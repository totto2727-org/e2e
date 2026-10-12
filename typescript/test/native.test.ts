import { once } from 'node:events'
import http, { type IncomingMessage, type ServerResponse } from 'node:http'

import tar from 'tar-stream'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { collect, Demultiplexer, DockerError, Engine } from '../src/engine.js'
import { DockerEnvironment, ExecutionError, type Command, type Environment } from '../src/environment.js'
import { DeadlineError, Lifecycle } from '../src/runner.js'

const disposals: (() => Promise<void>)[] = []
afterEach(async () => {
  vi.useRealTimers()
  for (const dispose of disposals.splice(0)) await dispose()
})

async function server(handler: (req: IncomingMessage, res: ServerResponse) => void): Promise<Engine> {
  const listener = http.createServer(handler)
  listener.listen(0, '127.0.0.1')
  await once(listener, 'listening')
  disposals.push(async () => {
    listener.closeAllConnections()
    listener.close()
    await once(listener, 'close')
  })
  const address = listener.address()
  if (!address || typeof address === 'string') throw new Error('Expected TCP address')
  return new Engine({ DOCKER_HOST: `http://127.0.0.1:${address.port}` })
}
function reply(res: ServerResponse, value: unknown, status = 200): void {
  res.writeHead(status, { 'Content-Type': 'application/json' })
  res.end(JSON.stringify(value))
}
function frame(stream: number, text: string | Buffer): Buffer {
  const body = Buffer.from(text)
  const header = Buffer.alloc(8)
  header[0] = stream
  header.writeUInt32BE(body.length, 4)
  return Buffer.concat([header, body])
}
async function commandEnvironment(
  options: { exit?: number; output?: Buffer; inspectStatus?: number; running?: boolean } = {},
): Promise<Environment> {
  let inspecting = 0
  const engine = await server((req, res) => {
    if (req.url?.endsWith('/exec')) reply(res, { Id: 'exec-id' }, 201)
    else if (req.url?.endsWith('/start')) {
      res.writeHead(200)
      res.end(options.output ?? frame(1, 'hello\n'))
    } else
      reply(
        res,
        { Running: options.running === true && inspecting++ === 0, ExitCode: options.exit ?? 0 },
        options.inspectStatus ?? 200,
      )
  })
  return new DockerEnvironment(engine, 'container', new AbortController().signal)
}

describe('Separate output streams', () => {
  test.each([1, 2, 3, 7, 8, 9, 16, 64])('decodes fragmented headers and UTF-8 bodies at chunk size %i', (size) => {
    const decoder = new Demultiplexer()
    const wire = Buffer.concat([frame(1, 'héllo\n'), frame(2, 'diagnostic\n'), frame(1, 'tail')])
    for (let offset = 0; offset < wire.length; offset += size) decoder.push(wire.subarray(offset, offset + size))
    decoder.finish()
    expect(decoder.output(true)).toEqual({ stdout: 'héllo\ntail', stderr: 'diagnostic\n' })
  })
  test('retains partial output on a truncated body', async () => {
    const wire = frame(1, 'hello')
    const env = await commandEnvironment({ output: wire.subarray(0, wire.length - 2) })
    await expect(env.run({ args: ['echo'] })).rejects.toMatchObject({
      name: 'ExecutionError',
      partialResult: { stdout: 'hel', stderr: '' },
      cause: { name: 'DockerError' },
    })
  })
  test('invalid stream header is infrastructure failure', async () => {
    const env = await commandEnvironment({ output: frame(3, 'wrong') })
    await expect(env.run({ args: ['echo'] })).rejects.toBeInstanceOf(ExecutionError)
  })
  test('invalid UTF-8 is a decode error without an exit code', async () => {
    const env = await commandEnvironment({ output: frame(1, Buffer.from([255])) })
    await expect(env.run({ args: ['echo'] })).rejects.toMatchObject({
      partialResult: { stdout: '�', stderr: '' },
      cause: { name: 'TypeError' },
    })
  })
})

describe('Commands', () => {
  test.each<Command>([
    { args: [] },
    { args: [''] },
    { args: ['x\0'] },
    { args: ['x', '\0'] },
    { args: ['x'], workingDir: '\0' },
    { args: ['x'], env: ['A=\0'] },
  ])('rejects invalid command before transport: %j', async (command) => {
    const env = new DockerEnvironment(
      {
        request: () => {
          throw new Error('Transport must not execute')
        },
      },
      'unused',
      new AbortController().signal,
    )
    await expect(env.run(command)).rejects.toBeInstanceOf(TypeError)
  })
  test('nonzero application exit is a result with distinct streams', async () => {
    const env = await commandEnvironment({ exit: 7, output: Buffer.concat([frame(1, 'out'), frame(2, 'err')]) })
    await expect(env.run({ args: ['tool'] })).resolves.toEqual({ exitCode: 7, stdout: 'out', stderr: 'err' })
  })
  test('preserves argv, empty arguments, working directory and environment', async () => {
    const command: Command = { args: ['tool', '', 'a b'], workingDir: '/work', env: ['A=two words', 'B='] }
    const engine = await server((req, res) => {
      if (req.url?.endsWith('/exec'))
        void collect(req).then((body) => {
          expect(JSON.parse(body.toString())).toEqual({
            Cmd: command.args,
            WorkingDir: '/work',
            Env: command.env,
            AttachStdout: true,
            AttachStderr: true,
            Tty: false,
          })
          reply(res, { Id: 'id' })
        })
      else if (req.url?.endsWith('/start')) {
        res.writeHead(200)
        res.end()
      } else reply(res, { Running: false, ExitCode: 0 })
    })
    await expect(new DockerEnvironment(engine, 'id', new AbortController().signal).run(command)).resolves.toEqual({
      exitCode: 0,
      stdout: '',
      stderr: '',
    })
  })
  test('waits for process exit even when output is already closed', async () => {
    const env = await commandEnvironment({ running: true, exit: 7 })
    await expect(env.run({ args: ['tool'] })).resolves.toEqual({ exitCode: 7, stdout: 'hello\n', stderr: '' })
  })
  test('inspection failures retain output and never report an exit code', async () => {
    const env = await commandEnvironment({ inspectStatus: 500 })
    await expect(env.run({ args: ['tool'] })).rejects.toMatchObject({
      partialResult: { stdout: 'hello\n', stderr: '' },
      cause: { statusCode: 500 },
    })
  })
  test('empty expected stderr asserts absence', async () => {
    const env = await commandEnvironment({ output: frame(2, 'diagnostic') })
    await expect(env.checkCommand({ args: ['tool'] }, { exitCode: 0, stdout: '', stderr: '' })).rejects.toThrow(
      'mismatch',
    )
  })
  test('daemon failures are never child stderr', async () => {
    const engine = await server((_req, res) => reply(res, { message: 'daemon failed' }, 500))
    await expect(
      new DockerEnvironment(engine, 'id', new AbortController().signal).run({ args: ['tool'] }),
    ).rejects.toMatchObject({ partialResult: { stdout: '', stderr: '' }, cause: { statusCode: 500 } })
  })
})

describe('HTTP cancellation and endpoint configuration', () => {
  test('rejects TLS instead of ignoring caller configuration', () => {
    expect(() => new Engine({ DOCKER_TLS_VERIFY: '1' })).toThrow(DockerError)
    expect(() => new Engine({ DOCKER_CERT_PATH: '/certs' })).toThrow(DockerError)
    expect(() => new Engine({ DOCKER_HOST: 'https://localhost:2376' })).toThrow(DockerError)
  })
  test('cancels an attached output stream and preserves received bytes', async () => {
    const controller = new AbortController()
    let disconnected!: () => void
    const closed = new Promise<void>((resolve) => {
      disconnected = resolve
    })
    const engine = await server((req, res) => {
      if (req.url?.endsWith('/exec')) reply(res, { Id: 'id' })
      else {
        res.writeHead(200)
        res.write(frame(1, 'partial'))
        res.on('close', disconnected)
      }
    })
    const transport = {
      async request(method: string, path: string, signal: AbortSignal, body?: Buffer) {
        const res = await engine.request(method, path, signal, body)
        if (path.endsWith('/start')) {
          const cancel = () => setImmediate(() => controller.abort(new DeadlineError()))
          if (res.readableLength > 0) cancel()
          else res.once('readable', cancel)
        }
        return res
      },
    }
    const execution = new DockerEnvironment(transport, 'id', controller.signal).run({ args: ['sleep', 'infinity'] })
    await expect(execution).rejects.toMatchObject({
      partialResult: { stdout: 'partial', stderr: '' },
      cause: { name: 'DeadlineError' },
    })
    await closed
  })
  test('cancels a request waiting for response headers', async () => {
    const controller = new AbortController()
    let received!: () => void
    const ready = new Promise<void>((resolve) => {
      received = resolve
    })
    const engine = await server(() => received())
    const assertion = expect(engine.request('GET', '/wait', controller.signal)).rejects.toThrow('cancelled')
    await ready
    controller.abort(new Error('cancelled'))
    await assertion
  })
})

async function lifecycle(fail?: 'create' | 'start' | 'cleanup'): Promise<{
  lifecycle: Lifecycle
  engine: Engine
  owned: Map<string, { Id: string; Config: { Labels: Record<string, string> } }>
  images: string[]
}> {
  const owned = new Map<string, { Id: string; Config: { Labels: Record<string, string> } }>()
  const images: string[] = []
  let created = 0
  const engine = await server((req, res) => {
    const url = new URL(req.url ?? '/', 'http://fixture')
    if (url.pathname.startsWith('/images/')) reply(res, { Id: 'sha256:local' })
    else if (url.pathname === '/containers/create')
      void collect(req).then((bytes) => {
        const body = JSON.parse(bytes.toString()) as { Image: string; Labels: Record<string, string> }
        images.push(body.Image)
        const value = { Id: `id-${++created}`, Config: { Labels: body.Labels } }
        owned.set(url.searchParams.get('name')!, value)
        reply(res, { Id: value.Id }, fail === 'create' && created > 1 ? 500 : 201)
      })
    else if (url.pathname.endsWith('/start')) reply(res, {}, fail === 'start' ? 500 : 204)
    else {
      const name = url.pathname.split('/')[2]!
      const value = owned.get(name)
      if (!value) reply(res, {}, 404)
      else if (req.method === 'DELETE') {
        if (fail === 'cleanup' && value.Id !== 'id-1') reply(res, {}, 500)
        else {
          owned.delete(name)
          res.writeHead(204)
          res.end()
        }
      } else reply(res, value)
    }
  })
  return { lifecycle: new Lifecycle(engine), engine, owned, images }
}

describe('Container ownership and lifecycle', () => {
  test.each(['create', 'start'] as const)('cleans partial %s failures and the image lease', async (failure) => {
    const { lifecycle: scope, owned, images } = await lifecycle(failure)
    await expect(scope.run('caller:local', async () => {})).rejects.toThrow(DockerError)
    expect(owned.size).toBe(0)
    expect(images).toEqual(['sha256:local', 'sha256:local'])
  })
  test('returns arbitrary callback results after cleanup and closes its signal', async () => {
    const { lifecycle: scope, owned } = await lifecycle()
    const result = { value: 42 }
    let signal!: AbortSignal
    await expect(
      scope.run('caller:local', async (env) => {
        signal = env.signal
        expect(owned.size).toBe(2)
        return result
      }),
    ).resolves.toBe(result)
    expect(owned.size).toBe(0)
    expect(signal.aborted).toBe(true)
  })
  test.each([new Error('assertion failure'), 'arbitrary failure', undefined])(
    'preserves callback rejection identity: %j',
    async (error) => {
      const { lifecycle: scope, owned } = await lifecycle()
      await expect(
        scope.run('caller:local', async () => {
          throw error
        }),
      ).rejects.toBe(error)
      expect(owned.size).toBe(0)
    },
  )
  test('reports cleanup failure but still removes the lease', async () => {
    const { lifecycle: scope, owned } = await lifecycle('cleanup')
    await expect(scope.run('caller:local', async () => {})).rejects.toThrow(DockerError)
    expect(owned.size).toBe(1)
    expect([...owned.values()].map((value) => value.Id)).toEqual(['id-2'])
  })
  test('retains callback and cleanup failures together', async () => {
    const { lifecycle: scope } = await lifecycle('cleanup')
    const failure = new Error('callback failed')
    await expect(
      scope.run('caller:local', async () => {
        throw failure
      }),
    ).rejects.toMatchObject({
      errors: [failure, expect.any(DockerError)],
    })
  })
  test('lets the consumer start more than two independent scopes without a queue', async () => {
    const { lifecycle: scope, owned, images } = await lifecycle()
    let entered = 0
    let ready!: () => void
    let release!: () => void
    const started = new Promise<void>((resolve) => {
      ready = resolve
    })
    const waiting = new Promise<void>((resolve) => {
      release = resolve
    })
    const executions = Array.from({ length: 3 }, () =>
      scope.run('caller:local', async () => {
        if (++entered === 3) ready()
        await waiting
      }),
    )
    await started
    expect(owned.size).toBe(6)
    expect(images).toEqual(Array<string>(6).fill('sha256:local'))
    release()
    await Promise.all(executions)
    expect(owned.size).toBe(0)
  })
  test('cancellation reaches callback code and cleanup has a fresh signal', async () => {
    const { lifecycle: scope, owned } = await lifecycle()
    const parent = new AbortController()
    const reason = new Error('cancelled by test')
    await expect(
      scope.run(
        'caller:local',
        async (env) => {
          parent.abort(reason)
          expect(env.signal.reason).toBe(reason)
          env.signal.throwIfAborted()
        },
        parent.signal,
      ),
    ).rejects.toBe(reason)
    expect(owned.size).toBe(0)
  })
})

test('the default deadline cancels operations and uses an independent cleanup budget', async () => {
  const { lifecycle: scope, owned } = await lifecycle()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })
  const execution = scope.run('caller:local', async (env) => {
    started()
    await once(env.signal, 'abort')
    env.signal.throwIfAborted()
  })
  const assertion = expect(execution).rejects.toBeInstanceOf(DeadlineError)
  await ready
  vi.advanceTimersByTime(60_000)
  await assertion
  expect(owned.size).toBe(0)
})

test('a missing local image fails without creating or pulling anything', async () => {
  const requests: string[] = []
  const engine = await server((req, res) => {
    requests.push(req.url ?? '')
    reply(res, {}, 404)
  })
  await expect(new Lifecycle(engine).run('missing:local', async () => {})).rejects.toMatchObject({ statusCode: 404 })
  expect(requests).toEqual(['/images/missing%3Alocal/json'])
})

test('a pre-cancelled invocation never contacts Docker', async () => {
  const reason = new Error('test cancelled before setup')
  const request = vi.fn(() => {
    throw new Error('Docker must not be contacted')
  })
  await expect(new Lifecycle({ request }).run('local', async () => {}, AbortSignal.abort(reason))).rejects.toBe(reason)
  expect(request).not.toHaveBeenCalled()
})

test('the operation budget includes image inspection before any container exists', async () => {
  let received!: () => void
  const ready = new Promise<void>((resolve) => {
    received = resolve
  })
  const engine = await server(() => received())
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const assertion = expect(new Lifecycle(engine).run('local', async () => {})).rejects.toBeInstanceOf(DeadlineError)
  await ready
  vi.advanceTimersByTime(60_000)
  await assertion
})

test('each cleanup stage has a fresh budget even after case removal times out', async () => {
  const { lifecycle: scope, owned, engine } = await lifecycle()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const original = engine.request.bind(engine)
  let removing!: () => void
  const ready = new Promise<void>((resolve) => {
    removing = resolve
  })
  const spy = vi.spyOn(engine, 'request').mockImplementation(async (method, path, signal, body) => {
    if (method === 'DELETE' && owned.get(path.split('/')[2]!.split('?')[0]!)?.Id === 'id-2') {
      removing()
      await once(signal, 'abort')
      signal.throwIfAborted()
    }
    return original(method, path, signal, body)
  })
  try {
    const assertion = expect(scope.run('local', async () => {})).rejects.toBeInstanceOf(DeadlineError)
    await ready
    vi.advanceTimersByTime(30_000)
    await assertion
    expect([...owned.values()].map((value) => value.Id)).toEqual(['id-2'])
  } finally {
    spy.mockRestore()
  }
})

test('refuses unowned case removal and still releases its matching lease', async () => {
  const { lifecycle: scope, owned } = await lifecycle()
  await expect(
    scope.run('local', async () => {
      const container = [...owned.values()].find((value) => value.Id === 'id-2')!
      container.Config.Labels = {}
    }),
  ).rejects.toThrow('Refusing to remove unowned container')
  expect([...owned.values()].map((value) => value.Id)).toEqual(['id-2'])
})

test('an unexpected HTTP 101 upgrade rejects instead of leaving an owned connection', async () => {
  const engine = await server((_req, res) => {
    res.writeHead(101, { Connection: 'Upgrade', Upgrade: 'tcp' })
    res.end()
  })
  await expect(engine.request('POST', '/exec/id/start', new AbortController().signal)).rejects.toThrow(
    'Unexpected Docker HTTP upgrade',
  )
})

test('valid UTF-8 preserves a leading byte order mark', async () => {
  const env = await commandEnvironment({ output: frame(1, '\ufefftext') })
  await expect(env.run({ args: ['tool'] })).resolves.toEqual({ exitCode: 0, stdout: '\ufefftext', stderr: '' })
})

describe('Physical path assertions', () => {
  test.each(['checkMissing', 'checkRegularFile', 'checkDirectory'] as const)(
    '%s rejects probe exit code 2',
    async (method) => {
      const env = await commandEnvironment({ exit: 2, output: frame(2, 'probe failed') })
      await expect(env[method]('/path')).rejects.toBeInstanceOf(DockerError)
    },
  )
  test.each(['checkMissing', 'checkRegularFile', 'checkDirectory'] as const)(
    '%s rejects a symbolic link even when its target matches',
    async (method) => {
      const env = await commandEnvironment({ exit: 0, output: Buffer.alloc(0) })
      await expect(env[method]('/path')).rejects.toThrow('Expected')
    },
  )
  test('symlink comparison requires its literal target', async () => {
    const env = await commandEnvironment({ output: frame(1, 'relative-target\n') })
    await expect(env.checkSymlink('/link', '/absolute-target')).rejects.toThrow('mismatch')
  })
})

test('archive writes preserve bytes and mode 0644, and reads return exact bytes', async () => {
  const bytes = Buffer.from([0, 255, 10, 13, 1])
  let archive: Buffer = Buffer.alloc(0)
  const engine = await server((req, res) => {
    if (req.method === 'PUT')
      void collect(req).then((body) => {
        archive = body
        res.writeHead(200)
        res.end()
      })
    else {
      res.writeHead(200)
      res.end(archive)
    }
  })
  const env = new DockerEnvironment(engine, 'id', new AbortController().signal)
  await env.writeFile('/workspace/binary', bytes)
  const extract = tar.extract()
  const header = new Promise<void>((resolve, reject) => {
    extract.on('error', reject)
    extract.on('entry', (entry, stream, next) => {
      expect(entry.mode).toBe(0o644)
      expect(entry.name).toBe('binary')
      stream.on('end', () => {
        next()
        resolve()
      })
      stream.resume()
    })
  })
  extract.end(archive)
  await header
  await expect(env.readFile('/workspace/binary')).resolves.toEqual(bytes)
  await expect(env.checkFile('/workspace/binary', Buffer.from('different'))).rejects.toThrow('mismatch')
})
