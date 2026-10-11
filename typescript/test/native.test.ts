import http, { type IncomingMessage, type ServerResponse } from 'node:http'
import { once } from 'node:events'

import { afterEach, describe, expect, test, vi } from 'vitest'
import tar from 'tar-stream'

import { collect, Demultiplexer, DockerError, Engine } from '../src/engine.js'
import { DockerEnvironment, ExecutionError, type Command, type Environment } from '../src/environment.js'
import { DeadlineError, Suite, validateCases } from '../src/runner.js'

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
  suite: Suite
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
  const suite = new Suite(engine)
  await suite.open('caller:local')
  return { suite, owned, images }
}

describe('Container ownership and lifecycle', () => {
  test('rejects duplicate or empty case names before setup', () => {
    const run = async () => {}
    expect(() => validateCases([{ name: '', run }])).toThrow(TypeError)
    expect(() =>
      validateCases([
        { name: 'same', run },
        { name: 'same', run },
      ]),
    ).toThrow(TypeError)
  })
  test.each(['create', 'start'] as const)(
    'cleans partial %s failures using pre-acquired ownership',
    async (failure) => {
      const { suite, owned, images } = await lifecycle(failure)
      await expect(suite.run(async () => {})).rejects.toThrow(DockerError)
      expect(owned.size).toBe(1)
      await suite.close()
      expect(owned.size).toBe(0)
      expect(images).toEqual(['sha256:local', 'sha256:local'])
    },
  )
  test('reports cleanup failure and still releases the slot', async () => {
    const { suite, owned } = await lifecycle('cleanup')
    for (let i = 0; i < 3; i++) await expect(suite.run(async () => {})).rejects.toThrow(DockerError)
    expect(owned.size).toBe(4)
    await suite.close()
  })
  test('starts budgets only after one of two slots is acquired', async () => {
    const { suite, owned } = await lifecycle()
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const controllers = [new AbortController(), new AbortController()]
    let entered = 0
    let both!: () => void
    const started = new Promise<void>((resolve) => {
      both = resolve
    })
    const blocking = async (env: Environment) => {
      entered++
      if (entered === 2) both()
      await once(env.signal, 'abort')
    }
    const first = suite.run(blocking, controllers[0]!.signal)
    const second = suite.run(blocking, controllers[1]!.signal)
    await started
    const third = suite.run(async (env) => {
      vi.advanceTimersByTime(2_000)
      expect(env.signal.aborted).toBe(false)
    })
    vi.advanceTimersByTime(59_000)
    expect(owned.size).toBe(3)
    controllers[0]!.abort()
    controllers[1]!.abort()
    await Promise.all([first, second, third])
    expect(owned.size).toBe(1)
    await suite.close()
    expect(owned.size).toBe(0)
  })
})

test('the default deadline cancels case operations and uses an independent cleanup budget', async () => {
  const { suite, owned } = await lifecycle()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })
  const execution = suite.run(async (env) => {
    started()
    await once(env.signal, 'abort')
    env.signal.throwIfAborted()
  })
  const assertion = expect(execution).rejects.toBeInstanceOf(DeadlineError)
  await ready
  vi.advanceTimersByTime(60_000)
  await assertion
  expect(owned.size).toBe(1)
  await suite.close()
  expect(owned.size).toBe(0)
})

test('a missing local image fails without creating or pulling anything', async () => {
  const requests: string[] = []
  const engine = await server((req, res) => {
    requests.push(req.url ?? '')
    reply(res, {}, 404)
  })
  await expect(new Suite(engine).open('missing:local')).rejects.toMatchObject({ statusCode: 404 })
  expect(requests).toEqual(['/images/missing%3Alocal/json'])
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
