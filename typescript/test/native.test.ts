import { once } from 'node:events'
import { PassThrough, Readable } from 'node:stream'

import tar from 'tar-stream'
import { log, type StartedTestContainer } from 'testcontainers'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import {
  DeadlineError,
  DockerError,
  FilesystemSnapshot,
  compareSnapshots,
  installDriver,
  withTerminal,
  type TerminalSession,
  withEnvironment,
  type Command,
  type Environment,
} from '../src/index.js'

const backend = vi.hoisted(() => {
  const container = {
    getId: () => 'case-container',
    exec: vi.fn<StartedTestContainer['exec']>(),
    copyContentToContainer: vi.fn<StartedTestContainer['copyContentToContainer']>(),
    copyArchiveFromContainer: vi.fn<StartedTestContainer['copyArchiveFromContainer']>(),
    stop: vi.fn<StartedTestContainer['stop']>(),
  }
  return { container, start: vi.fn(async () => container) }
})

// Substitute only the external Testcontainers boundary, not Docker's wire protocol.
vi.mock('testcontainers', async (importOriginal) => {
  const actual = await importOriginal<typeof import('testcontainers')>()
  return {
    ...actual,
    GenericContainer: vi.fn(function () {
      return {
        withLabels: vi.fn().mockReturnThis(),
        withPullPolicy: vi.fn().mockReturnThis(),
        withStartupTimeout: vi.fn().mockReturnThis(),
        start: backend.start,
      }
    }),
  }
})

const empty = { exitCode: 0, stdout: '', stderr: '' }
beforeEach(() => {
  backend.start.mockReset().mockResolvedValue(backend.container)
  backend.container.exec.mockReset().mockResolvedValue({ ...empty, output: '' })
  backend.container.copyContentToContainer.mockReset().mockResolvedValue()
  backend.container.copyArchiveFromContainer.mockReset()
  backend.container.stop.mockReset().mockResolvedValue({
    getId: () => 'stopped',
    copyArchiveFromContainer: backend.container.copyArchiveFromContainer,
  })
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

async function scoped(action: (environment: Environment) => Promise<void>): Promise<void> {
  await withEnvironment('caller:local', action)
}

function archive(entries: { name: string; bytes?: Buffer; type?: 'file' | 'directory' | 'symlink' }[]): Readable {
  const pack = tar.pack()
  for (const entry of entries)
    pack.entry({ name: entry.name, type: entry.type ?? 'file', linkname: 'target' }, entry.bytes ?? Buffer.alloc(0))
  pack.finalize()
  return Readable.from(pack)
}

describe('Commands', () => {
  test.each<Command>([
    { args: [] },
    { args: [''] },
    { args: ['x\0'] },
    { args: ['x', '\0'] },
    { args: ['x'], workingDir: '\0' },
    { args: ['x'], env: ['A=\0'] },
    { args: ['x'], env: ['MISSING_EQUALS'] },
    { args: ['x'], env: ['=missing-name'] },
  ])('rejects invalid command before execution: %j', async (command) => {
    await scoped(async (env) => {
      await expect(env.run(command)).rejects.toBeInstanceOf(TypeError)
    })
    expect(backend.container.exec).not.toHaveBeenCalled()
  })
  test('nonzero application exit is a result with distinct streams', async () => {
    backend.container.exec.mockResolvedValue({ exitCode: 7, stdout: 'out', stderr: 'err', output: 'outerr' })
    await scoped(async (env) => {
      await expect(env.run({ args: ['tool'] })).resolves.toEqual({ exitCode: 7, stdout: 'out', stderr: 'err' })
    })
  })
  test('preserves argv, empty arguments, working directory and environment values containing equals', async () => {
    await scoped(async (env) => {
      await env.run({ args: ['tool', '', 'a b'], workingDir: '/work', env: ['A=two words', 'B=', 'C=a=b'] })
    })
    expect(backend.container.exec).toHaveBeenCalledWith(['tool', '', 'a b'], {
      workingDir: '/work',
      env: { A: 'two words', B: '', C: 'a=b' },
    })
  })
  test('keeps special environment names and lets the last duplicate win', async () => {
    await scoped(async (env) => {
      await env.run({ args: ['tool'], env: ['__proto__=safe', 'constructor=value', 'A=old', 'A=new'] })
    })
    expect(backend.container.exec).toHaveBeenCalledWith(['tool'], {
      env: { ['__proto__']: 'safe', constructor: 'value', A: 'new' },
    })
  })
  test.each([-1, Number.NaN, 0.5])('rejects unavailable or invalid application exit code %j', async (exitCode) => {
    backend.container.exec.mockResolvedValue({ ...empty, output: '', exitCode })
    await scoped(async (env) => {
      await expect(env.run({ args: ['tool'] })).rejects.toMatchObject({
        name: 'ExecutionError',
        partialResult: { stdout: '', stderr: '' },
        cause: { name: 'DockerError' },
      })
    })
  })
  test('infrastructure diagnostics never become child stderr or an application exit', async () => {
    const cause = Object.assign(new Error('daemon failed'), { statusCode: 500 })
    backend.container.exec.mockRejectedValue(cause)
    await scoped(async (env) => {
      await expect(env.run({ args: ['tool'] })).rejects.toMatchObject({
        name: 'ExecutionError',
        partialResult: { stdout: '', stderr: '' },
        cause: { name: 'DockerError', statusCode: 500, cause },
      })
    })
  })
  test('empty expected stderr asserts absence', async () => {
    backend.container.exec.mockResolvedValue({ ...empty, output: 'diagnostic', stderr: 'diagnostic' })
    await scoped(async (env) => {
      await expect(env.checkCommand({ args: ['tool'] }, empty)).rejects.toThrow('mismatch')
    })
  })
  test('returns the decoded text supplied by Testcontainers without trimming', async () => {
    const stdout = '\ufeffhéllo\n'
    backend.container.exec.mockResolvedValue({ ...empty, stdout, output: stdout })
    await scoped(async (env) => {
      await expect(env.run({ args: ['tool'] })).resolves.toEqual({ ...empty, stdout })
    })
  })
})

describe('Scope lifecycle', () => {
  test('returns arbitrary callback results after cleanup and closes its signal', async () => {
    const value = { value: 42 }
    let signal!: AbortSignal
    await expect(
      withEnvironment('local', async (env) => {
        signal = env.signal
        return value
      }),
    ).resolves.toBe(value)
    expect(signal.aborted).toBe(true)
    expect(backend.container.stop).toHaveBeenCalledWith({ timeout: 0, remove: true, removeVolumes: true })
  })
  test.each([new Error('assertion failure'), 'arbitrary failure', undefined])(
    'preserves callback rejection identity: %j',
    async (failure) => {
      await expect(
        withEnvironment('local', async () => {
          throw failure
        }),
      ).rejects.toBe(failure)
      expect(backend.container.stop).toHaveBeenCalledOnce()
    },
  )
  test('retains callback and cleanup failures together', async () => {
    const failure = new Error('callback failed')
    const cleanup = new Error('cleanup failed')
    backend.container.stop.mockRejectedValue(cleanup)
    await expect(
      withEnvironment('local', async () => {
        throw failure
      }),
    ).rejects.toMatchObject({
      name: 'AggregateError',
      errors: [failure, expect.objectContaining({ name: 'DockerError', cause: cleanup })],
    })
  })
  test('reports cleanup failure alone', async () => {
    const cause = new Error('cleanup failed')
    backend.container.stop.mockRejectedValue(cause)
    await expect(scoped(async () => {})).rejects.toMatchObject({ name: 'DockerError', cause })
  })
  test.each(['', 'image\0name'])('rejects invalid image %j before startup', async (image) => {
    await expect(withEnvironment(image, async () => {})).rejects.toBeInstanceOf(TypeError)
    expect(backend.start).not.toHaveBeenCalled()
  })
  test('a pre-cancelled invocation never starts Testcontainers', async () => {
    const reason = new Error('cancelled before setup')
    await expect(withEnvironment('local', async () => {}, AbortSignal.abort(reason))).rejects.toBe(reason)
    expect(backend.start).not.toHaveBeenCalled()
  })
  test('startup failures retain their cause without invoking the callback', async () => {
    const cause = new Error('missing local image')
    backend.start.mockRejectedValue(cause)
    const action = vi.fn(async () => {})
    await expect(withEnvironment('missing', action)).rejects.toMatchObject({ name: 'DockerError', cause })
    expect(action).not.toHaveBeenCalled()
  })
  test('cancellation reaches callback code and cleanup remains independent', async () => {
    const controller = new AbortController()
    const reason = new Error('cancelled by test')
    await expect(
      withEnvironment(
        'local',
        async (env) => {
          controller.abort(reason)
          expect(env.signal.reason).toBe(reason)
        },
        controller.signal,
      ),
    ).rejects.toBe(reason)
    expect(backend.container.stop).toHaveBeenCalledOnce()
  })
  test('command timeout releases the scope even when Testcontainers exec never resolves', async () => {
    vi.useFakeTimers()
    backend.container.exec.mockReturnValue(new Promise(() => {}))
    const ready = Promise.withResolvers<void>()
    const execution = scoped(async (env) => {
      ready.resolve()
      await env.run({ args: ['sleep', 'infinity'] })
    })
    const assertion = expect(execution).rejects.toMatchObject({
      name: 'ExecutionError',
      cause: { name: 'DeadlineError' },
      partialResult: { stdout: '', stderr: '' },
    })
    await ready.promise
    await vi.advanceTimersByTimeAsync(60_000)
    await assertion
    expect(backend.container.stop).toHaveBeenCalledOnce()
  })
  test('the startup wait has a deadline and removes a container that resolves late', async () => {
    vi.useFakeTimers()
    const starting = Promise.withResolvers<typeof backend.container>()
    backend.start.mockReturnValue(starting.promise)
    const action = vi.fn(async () => {})
    const assertion = expect(withEnvironment('local', action)).rejects.toBeInstanceOf(DeadlineError)
    await vi.advanceTimersByTimeAsync(60_000)
    await assertion
    expect(action).not.toHaveBeenCalled()
    starting.resolve(backend.container)
    await vi.advanceTimersByTimeAsync(0)
    expect(backend.container.stop).toHaveBeenCalledOnce()
  })
  test('late-start cleanup failures produce a Testcontainers diagnostic', async () => {
    vi.useFakeTimers()
    const starting = Promise.withResolvers<typeof backend.container>()
    backend.start.mockReturnValue(starting.promise)
    backend.container.stop.mockRejectedValue(new Error('late cleanup failed'))
    const diagnostic = vi.spyOn(log, 'error').mockImplementation(() => {})
    const assertion = expect(scoped(async () => {})).rejects.toBeInstanceOf(DeadlineError)
    await vi.advanceTimersByTimeAsync(60_000)
    await assertion
    starting.resolve(backend.container)
    await vi.advanceTimersByTimeAsync(0)
    expect(diagnostic).toHaveBeenCalledWith(expect.stringContaining('Late-start container cleanup failed'), {
      containerId: 'case-container',
    })
  })
  test('cleanup waiting has its own fresh 30-second budget', async () => {
    vi.useFakeTimers()
    backend.container.stop.mockReturnValue(new Promise(() => {}))
    const assertion = expect(scoped(async () => {})).rejects.toBeInstanceOf(DeadlineError)
    await vi.advanceTimersByTimeAsync(30_000)
    await assertion
  })
  test('does not add a fixed concurrency queue', async () => {
    const ready = Promise.withResolvers<void>()
    const release = Promise.withResolvers<void>()
    let entered = 0
    const scopes = Array.from({ length: 3 }, () =>
      scoped(async () => {
        if (++entered === 3) ready.resolve()
        await release.promise
      }),
    )
    await ready.promise
    expect(entered).toBe(3)
    release.resolve()
    await Promise.all(scopes)
  })
})

describe('File operations', () => {
  test.each(['', 'bad\0path'])('rejects invalid file path %j before transfer', async (path) => {
    await scoped(async (env) => {
      await expect(env.writeFile(path, Buffer.alloc(0))).rejects.toBeInstanceOf(TypeError)
      await expect(env.readFile(path)).rejects.toBeInstanceOf(TypeError)
    })
    expect(backend.container.copyContentToContainer).not.toHaveBeenCalled()
    expect(backend.container.copyArchiveFromContainer).not.toHaveBeenCalled()
  })
  test('writes exact bytes and mode 0644 through Testcontainers', async () => {
    const bytes = Buffer.from([0, 255, 10, 13, 1])
    await scoped(async (env) => {
      await env.writeFile('/workspace/binary', bytes)
    })
    expect(backend.container.copyContentToContainer).toHaveBeenCalledWith([
      { target: '/workspace/binary', content: bytes, mode: 0o644 },
    ])
  })
  test('does not create missing parent directories', async () => {
    backend.container.exec.mockResolvedValue({ ...empty, output: '', exitCode: 1 })
    await scoped(async (env) => {
      await expect(env.writeFile('/missing/file', Buffer.alloc(0))).rejects.toThrow('Parent directory')
    })
    expect(backend.container.copyContentToContainer).not.toHaveBeenCalled()
  })
  test('reads exact bytes from the archive supplied by Testcontainers', async () => {
    const bytes = Buffer.from([0, 255, 10, 13, 1])
    backend.container.copyArchiveFromContainer.mockResolvedValue(archive([{ name: 'binary', bytes }]))
    await scoped(async (env) => {
      await expect(env.readFile('/workspace/binary')).resolves.toEqual(bytes)
    })
  })
  test('reports file comparison mismatches', async () => {
    backend.container.copyArchiveFromContainer.mockResolvedValue(
      archive([{ name: 'file', bytes: Buffer.from('actual') }]),
    )
    await scoped(async (env) => {
      await expect(env.checkFile('/file', Buffer.from('expected'))).rejects.toThrow('mismatch')
    })
  })
  test.each([
    { entries: [] },
    { entries: [{ name: 'dir', type: 'directory' as const }] },
    { entries: [{ name: 'link', type: 'symlink' as const }] },
    { entries: [{ name: 'one' }, { name: 'two' }] },
  ])('rejects archives other than one regular file: $entries', async ({ entries }) => {
    backend.container.copyArchiveFromContainer.mockResolvedValue(archive(entries))
    await scoped(async (env) => {
      await expect(env.readFile('/file')).rejects.toBeInstanceOf(DockerError)
    })
  })
  test('archive transfer errors retain their original cause', async () => {
    const cause = new Error('archive transfer failed')
    backend.container.copyArchiveFromContainer.mockRejectedValue(cause)
    await scoped(async (env) => {
      await expect(env.readFile('/file')).rejects.toMatchObject({ name: 'DockerError', cause })
    })
  })
  test('write transfer errors retain their original cause', async () => {
    const cause = new Error('copy failed')
    backend.container.copyContentToContainer.mockRejectedValue(cause)
    await scoped(async (env) => {
      await expect(env.writeFile('/file', Buffer.alloc(0))).rejects.toMatchObject({ name: 'DockerError', cause })
    })
  })
  test('truncated archive streams reject instead of producing partial file bytes', async () => {
    backend.container.copyArchiveFromContainer.mockResolvedValue(Readable.from([Buffer.from('not a tar archive')]))
    await scoped(async (env) => {
      await expect(env.readFile('/file')).rejects.toThrow()
    })
  })
  test('closes archive responses that arrive after cancellation', async () => {
    const controller = new AbortController()
    const copying = Promise.withResolvers<NodeJS.ReadableStream>()
    backend.container.copyArchiveFromContainer.mockReturnValue(copying.promise)
    const stream = new PassThrough()
    const reason = new Error('cancel before archive response')
    await expect(
      withEnvironment(
        'local',
        async (env) => {
          const assertion = expect(env.readFile('/file')).rejects.toBe(reason)
          controller.abort(reason)
          await assertion
          copying.resolve(stream)
          await Promise.resolve()
        },
        controller.signal,
      ),
    ).rejects.toBe(reason)
    expect(stream.destroyed).toBe(true)
  })
  test('cancellation closes an archive stream already returned by Testcontainers', async () => {
    const controller = new AbortController()
    const stream = new PassThrough()
    backend.container.copyArchiveFromContainer.mockResolvedValue(stream)
    const reason = new Error('cancel read')
    await expect(
      withEnvironment(
        'local',
        async (env) => {
          const piping = once(stream, 'resume')
          const assertion = expect(env.readFile('/file')).rejects.toBe(reason)
          await piping
          controller.abort(reason)
          await assertion
        },
        controller.signal,
      ),
    ).rejects.toBe(reason)
    expect(stream.destroyed).toBe(true)
  })
})

describe('Physical path assertions', () => {
  test.each(['checkMissing', 'checkRegularFile', 'checkDirectory'] as const)(
    '%s rejects probe exit code 2',
    async (method) => {
      backend.container.exec.mockResolvedValue({
        exitCode: 2,
        stdout: '',
        stderr: 'probe failed',
        output: 'probe failed',
      })
      await scoped(async (env) => {
        await expect(env[method]('/path')).rejects.toBeInstanceOf(DockerError)
      })
    },
  )
  test.each(['checkMissing', 'checkRegularFile', 'checkDirectory'] as const)(
    '%s rejects a symbolic link even if its target matches',
    async (method) => {
      await scoped(async (env) => {
        await expect(env[method]('/path')).rejects.toThrow('Expected')
      })
    },
  )
  test('symlink comparison requires its literal target', async () => {
    backend.container.exec.mockResolvedValue({ ...empty, stdout: 'relative-target\n', output: 'relative-target\n' })
    await scoped(async (env) => {
      await expect(env.checkSymlink('/link', '/absolute-target')).rejects.toThrow('mismatch')
    })
  })
})

async function installMock(env: Environment): Promise<void> {
  backend.container.exec
    .mockResolvedValueOnce({ ...empty, stdout: '/tmp/caseyard-driver-test\n', output: '' })
    .mockResolvedValueOnce({ ...empty, output: '' })
  await installDriver(env, Buffer.from('executable'))
}

async function driverScoped(action: (env: Environment) => Promise<void>): Promise<void> {
  await scoped(async (env) => {
    await installMock(env)
    await action(env)
  })
}

function driverResult(value: unknown) {
  return { ...empty, stdout: JSON.stringify({ ok: true, value }), output: '' }
}

const terminalObservation = {
  stdout: [0, 255, 27],
  terminal: [27, 91, 65],
  unreadInput: [10],
  status: { kind: 'exited', code: 7 },
  termiosRestored: true,
  initialTermios: [1, [2, 3]],
  currentTermios: [1, [2, 3]],
}

function terminalBackend(value: unknown = terminalObservation): void {
  backend.container.exec.mockImplementation(async (args) => {
    if (args[1] === 'start') return driverResult({ path: '/tmp/caseyard-pty-test/socket' })
    const request: unknown = JSON.parse(args[2] ?? '{}')
    if (typeof request === 'object' && request !== null && 'operation' in request && request.operation === 'close')
      return driverResult(null)
    return driverResult(value)
  })
}

describe('Static driver installation', () => {
  test('concurrent install attempts cannot stage or overwrite a second driver', async () => {
    const staging = Promise.withResolvers<Awaited<ReturnType<StartedTestContainer['exec']>>>()
    backend.container.exec.mockReturnValueOnce(staging.promise)
    await scoped(async (env) => {
      const first = installDriver(env, Buffer.from('first'))
      await expect(installDriver(env, Buffer.from('second'))).rejects.toThrow('already installed')
      staging.resolve({ ...empty, stdout: '/tmp/caseyard-driver-test\n', output: '' })
      await first
    })
    expect(backend.container.copyContentToContainer).toHaveBeenCalledExactlyOnceWith([
      { target: '/tmp/caseyard-driver-test/driver', content: Buffer.from('first'), mode: 0o755 },
    ])
  })

  test('failed installation does not publish a usable driver', async () => {
    backend.container.exec.mockResolvedValue({ ...empty, output: '', exitCode: 1, stderr: 'mktemp denied' })
    await scoped(async (env) => {
      await expect(installDriver(env, Buffer.from('executable'))).rejects.toThrow('mktemp denied')
      await expect(env.snapshot(['/path'])).rejects.toThrow('installDriver')
    })
    expect(backend.container.copyContentToContainer).not.toHaveBeenCalled()
  })

  test('driver APIs reject use before installation', async () => {
    await scoped(async (env) => {
      await expect(env.snapshot(['/file'])).rejects.toThrow('installDriver')
      await expect(withTerminal(env, { args: ['tool'] }, {}, async () => {})).rejects.toThrow('installDriver')
    })
    expect(backend.container.exec).not.toHaveBeenCalled()
  })

  test('stages exact bytes privately, makes them executable, and rejects a second install', async () => {
    await driverScoped(async (env) => {
      await expect(installDriver(env, Buffer.from('other'))).rejects.toThrow('already installed')
    })
    expect(backend.container.copyContentToContainer).toHaveBeenCalledWith([
      { target: '/tmp/caseyard-driver-test/driver', content: Buffer.from('executable'), mode: 0o755 },
    ])
    expect(backend.container.exec.mock.calls.some(([args]) => args[0] === 'chmod')).toBe(false)
  })

  test('empty driver bytes fail before staging', async () => {
    await scoped(async (env) => {
      await expect(installDriver(env, Buffer.alloc(0))).rejects.toBeInstanceOf(TypeError)
    })
    expect(backend.container.exec).not.toHaveBeenCalled()
  })
})

describe('Scoped terminals', () => {
  test('terminal close already in progress survives parent cancellation', async () => {
    terminalBackend()
    const controller = new AbortController()
    const reason = new Error('cancel during terminal close')
    const closing = Promise.withResolvers<void>()
    const closed = Promise.withResolvers<Awaited<ReturnType<StartedTestContainer['exec']>>>()
    const execution = withEnvironment(
      'local',
      async (env) => {
        await installMock(env)
        await withTerminal(env, { args: ['tool'] }, {}, async () => {
          backend.container.exec.mockImplementationOnce(() => {
            closing.resolve()
            return closed.promise
          })
        })
      },
      controller.signal,
    )
    const assertion = expect(execution).rejects.toBe(reason)
    await closing.promise
    controller.abort(reason)
    closed.resolve(driverResult(null))
    await assertion
    expect(backend.container.stop).toHaveBeenCalledOnce()
  })

  test('terminal cleanup gets a fresh thirty seconds near the environment deadline', async () => {
    vi.useFakeTimers()
    terminalBackend()
    const closing = Promise.withResolvers<void>()
    const execution = scoped(async (env) => {
      await installMock(env)
      await withTerminal(env, { args: ['tool'] }, {}, async () => {
        await vi.advanceTimersByTimeAsync(59_999)
        backend.container.exec.mockImplementationOnce(() => {
          closing.resolve()
          return new Promise(() => {})
        })
      })
    })
    const assertion = expect(execution).rejects.toMatchObject({
      name: 'ExecutionError',
      cause: { message: 'Terminal cleanup deadline exceeded' },
    })
    await closing.promise
    await vi.advanceTimersByTimeAsync(29_999)
    expect(backend.container.stop).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    await assertion
    expect(backend.container.stop).toHaveBeenCalledOnce()
  })

  test('cancellation rejects a pending request, invalidates the session and uses container teardown', async () => {
    terminalBackend()
    const controller = new AbortController()
    const reason = new Error('cancel terminal')
    let escaped!: TerminalSession
    await expect(
      withEnvironment(
        'local',
        async (env) => {
          await installMock(env)
          await withTerminal(env, { args: ['tool'] }, {}, async (session) => {
            escaped = session
            backend.container.exec.mockReturnValueOnce(new Promise(() => {}))
            const assertion = expect(session.observe()).rejects.toMatchObject({ name: 'ExecutionError', cause: reason })
            controller.abort(reason)
            await assertion
          })
        },
        controller.signal,
      ),
    ).rejects.toBe(reason)
    await expect(escaped.observe()).rejects.toThrow('closed')
    expect(backend.container.stop).toHaveBeenCalledOnce()
    expect(backend.container.exec.mock.calls.some(([args]) => args[2]?.includes('"operation":"close"'))).toBe(false)
  })

  test('invalid operation bounds reject as promises without invoking the driver', async () => {
    terminalBackend()
    await driverScoped(async (env) => {
      await withTerminal(env, { args: ['tool'] }, {}, async (session) => {
        const calls = backend.container.exec.mock.calls.length
        await expect(session.expect('stdout', Buffer.alloc(0))).rejects.toBeInstanceOf(TypeError)
        await expect(session.expect('stdout', Buffer.from('x'), 0)).rejects.toBeInstanceOf(TypeError)
        await expect(session.resize(0, 80)).rejects.toBeInstanceOf(TypeError)
        await expect(session.wait(Number.POSITIVE_INFINITY)).rejects.toBeInstanceOf(TypeError)
        expect(backend.container.exec).toHaveBeenCalledTimes(calls)
      })
    })
  })

  test('preserves raw bytes and normalized settings, forwards command context, closes and invalidates the handle', async () => {
    terminalBackend()
    let escaped!: TerminalSession
    await driverScoped(async (env) => {
      await expect(
        withTerminal(env, { args: ['tool', ''], workingDir: '/workspace', env: ['A=b=c'] }, {}, async (session) => {
          escaped = session
          expect(await session.observe()).toEqual({
            ...terminalObservation,
            stdout: Buffer.from([0, 255, 27]),
            terminal: Buffer.from([27, 91, 65]),
            unreadInput: Buffer.from([10]),
          })
          await session.expect('terminal', Buffer.from([27]), 1)
          await session.send(Buffer.from([3, 4]))
          await session.resize(20, 80)
          await session.signal('SIGINT')
          await session.wait(1)
          return 42
        }),
      ).resolves.toBe(42)
      await expect(escaped.observe()).rejects.toThrow('closed')
    })
    expect(backend.container.exec).toHaveBeenCalledWith(
      [
        '/tmp/caseyard-driver-test/driver',
        'start',
        JSON.stringify({ argv: ['tool', ''], rows: 40, columns: 120, timeout: 50, input: null }),
      ],
      { workingDir: '/workspace', env: { A: 'b=c' } },
    )
    expect(backend.container.exec).toHaveBeenLastCalledWith(
      [
        '/tmp/caseyard-driver-test/driver',
        'request',
        JSON.stringify({ path: '/tmp/caseyard-pty-test/socket', operation: 'close' }),
      ],
      { env: {} },
    )
  })

  test.each([new Error('callback failure'), undefined])(
    'closes after arbitrary callback rejection %j',
    async (cause) => {
      terminalBackend()
      await driverScoped(async (env) => {
        await expect(
          withTerminal(env, { args: ['tool'] }, {}, async () => {
            throw cause
          }),
        ).rejects.toBe(cause)
      })
      expect(backend.container.exec).toHaveBeenLastCalledWith(
        expect.arrayContaining([expect.stringContaining('"operation":"close"')]),
        { env: {} },
      )
    },
  )

  test('retains callback and close failures together', async () => {
    backend.container.exec.mockImplementation(async (args) =>
      args[1] === 'start'
        ? driverResult({ path: '/tmp/caseyard-pty-test/socket' })
        : {
            ...empty,
            exitCode: 1,
            stdout: JSON.stringify({ ok: false, error: 'close failed', observation: null }),
            output: '',
          },
    )
    const cause = new Error('callback failure')
    await driverScoped(async (env) => {
      await expect(
        withTerminal(env, { args: ['tool'] }, {}, async () => {
          throw cause
        }),
      ).rejects.toMatchObject({
        name: 'AggregateError',
        errors: [cause, expect.objectContaining({ name: 'TerminalError', message: 'close failed' })],
      })
    })
  })

  test('pipe stdin keeps unread bytes and rejects interactive sends', async () => {
    terminalBackend()
    await driverScoped(async (env) => {
      await withTerminal(env, { args: ['tool'] }, { input: Buffer.from([0, 255]) }, async (session) => {
        await expect(session.send(Buffer.from('x'))).rejects.toThrow('pipe')
        expect((await session.wait()).unreadInput).toEqual(Buffer.from([10]))
      })
    })
  })

  test.each([{ rows: 0 }, { columns: 65536 }, { rows: 0.5 }, { timeout: 0 }, { timeout: Number.NaN }, { timeout: 61 }])(
    'invalid terminal options fail before start %j',
    async (options) => {
      await scoped(async (env) => {
        await expect(withTerminal(env, { args: ['tool'] }, options, async () => {})).rejects.toBeInstanceOf(TypeError)
      })
      expect(backend.container.exec).not.toHaveBeenCalled()
    },
  )

  test.each([
    { ...terminalObservation, stdout: [256] },
    { ...terminalObservation, terminal: [-1] },
    { ...terminalObservation, unreadInput: [1.5] },
    { ...terminalObservation, status: { kind: 'exited', code: -1 } },
    { ...terminalObservation, status: { kind: 'running', code: 7 } },
    { ...terminalObservation, status: { kind: 'exited', code: 7, signal: 15 } },
    { ...terminalObservation, status: { kind: 'signaled', signal: 15, code: 7 } },
    { ...terminalObservation, termiosRestored: 'true' },
    { ...terminalObservation, currentTermios: ['invalid'] },
  ])('rejects malformed observation %j', async (value) => {
    terminalBackend(value)
    await driverScoped(async (env) => {
      await withTerminal(env, { args: ['tool'] }, {}, async (session) => {
        await expect(session.observe()).rejects.toThrow()
      })
    })
  })

  test('expect failures retain captured raw output', async () => {
    terminalBackend()
    await driverScoped(async (env) => {
      await withTerminal(env, { args: ['tool'] }, {}, async (session) => {
        backend.container.exec.mockResolvedValueOnce({
          ...empty,
          exitCode: 1,
          stdout: JSON.stringify({
            ok: false,
            error: 'waiting for terminal bytes',
            observation: terminalObservation,
          }),
          output: '',
        })
        await expect(session.expect('terminal', Buffer.from('absent'), 1)).rejects.toMatchObject({
          name: 'TerminalError',
          observation: { stdout: Buffer.from([0, 255, 27]), terminal: Buffer.from([27, 91, 65]) },
        })
      })
    })
  })
})

describe('Filesystem snapshots', () => {
  test('defensively copies arbitrary link target bytes on construction and observation', () => {
    const target = Buffer.from([255, 10])
    const snapshot = new FilesystemSnapshot([{ path: '/link', state: { kind: 'symlink', target } }])
    target.fill(0)
    const state = snapshot.entries[0]!.state
    if (state.kind !== 'symlink') throw new Error('Expected symlink')
    state.target.fill(1)
    expect(snapshot.entries).toEqual([{ path: '/link', state: { kind: 'symlink', target: Buffer.from([255, 10]) } }])
  })

  test('empty explicit path sets do not execute or require driver installation', async () => {
    await scoped(async (env) => {
      expect((await env.snapshot([])).entries).toEqual([])
    })
    expect(backend.container.exec).not.toHaveBeenCalled()
  })

  test.each([
    { path: '/path', kind: 'socket', data: [], target: [] },
    { path: '/path', kind: 'file', data: [256], target: [] },
    { path: '/path', kind: 'symlink', data: [], target: [-1] },
    { path: '/path', kind: 'directory', data: [1], target: [] },
    { path: '/other', kind: 'missing', data: [], target: [] },
  ])('rejects invalid snapshot wire entry %j', async (entry) => {
    backend.container.exec.mockResolvedValue(driverResult([entry]))
    await driverScoped(async (env) => {
      await expect(env.snapshot(['/path'])).rejects.toThrow()
    })
  })

  test('literal targets preserve invalid UTF-8, byte-order markers and trailing newlines', async () => {
    const target = Buffer.concat([Buffer.from([255]), Buffer.from('\ufefftarget\n')])
    backend.container.exec.mockResolvedValue(
      driverResult([{ path: '/link', kind: 'symlink', data: [], target: [...Buffer.from(target)] }]),
    )
    await driverScoped(async (env) => {
      expect((await env.snapshot(['/link'])).entries).toEqual([{ path: '/link', state: { kind: 'symlink', target } }])
    })
  })

  test('captures sorted explicit paths, exact bytes, directories and literal dangling links', async () => {
    const bytes = Buffer.from([0, 255, 13, 10])
    backend.container.exec.mockResolvedValue(
      driverResult([
        { path: '/dir', kind: 'directory', data: [], target: [] },
        { path: '/file', kind: 'file', data: [...bytes], target: [] },
        { path: '/link', kind: 'symlink', data: [], target: [...Buffer.from('../absent\n')] },
        { path: '/missing', kind: 'missing', data: [], target: [] },
      ]),
    )
    await driverScoped(async (env) => {
      expect((await env.snapshot(['/missing', '/link', '/file', '/dir', '/file'])).entries).toEqual([
        { path: '/dir', state: { kind: 'directory' } },
        { path: '/file', state: { kind: 'file', bytes } },
        { path: '/link', state: { kind: 'symlink', target: Buffer.from('../absent\n') } },
        { path: '/missing', state: { kind: 'missing' } },
      ])
    })
    expect(backend.container.copyArchiveFromContainer).not.toHaveBeenCalled()
  })

  test('detaches input and output byte buffers', () => {
    const bytes = Buffer.from([0, 255])
    const snapshot = new FilesystemSnapshot([{ path: '/file', state: { kind: 'file', bytes } }])
    bytes.fill(1)
    const state = snapshot.entries[0]!.state
    if (state.kind !== 'file') throw new Error('Expected file')
    state.bytes.fill(2)
    expect(snapshot.entries).toEqual([{ path: '/file', state: { kind: 'file', bytes: Buffer.from([0, 255]) } }])
    expect(Object.isFrozen(snapshot.entries)).toBe(true)
  })

  test('returns deterministic existence, kind, content and target differences without changing snapshots', () => {
    const expected = new FilesystemSnapshot([
      { path: '/target', state: { kind: 'symlink', target: Buffer.from('before') } },
      { path: '/kind', state: { kind: 'directory' } },
      { path: '/exists', state: { kind: 'missing' } },
      { path: '/content', state: { kind: 'file', bytes: Buffer.from([0, 255]) } },
    ])
    const actual = new FilesystemSnapshot([
      { path: '/content', state: { kind: 'file', bytes: Buffer.from([0, 254]) } },
      { path: '/exists', state: { kind: 'directory' } },
      { path: '/kind', state: { kind: 'symlink', target: Buffer.from('directory') } },
      { path: '/target', state: { kind: 'symlink', target: Buffer.from('after') } },
    ])
    expect(compareSnapshots(expected, actual)).toEqual([
      { path: '/content', reason: 'content' },
      { path: '/exists', reason: 'existence' },
      { path: '/kind', reason: 'kind' },
      { path: '/target', reason: 'target' },
    ])
    expect(compareSnapshots(expected, expected)).toEqual([])
    expect(compareSnapshots(new FilesystemSnapshot([]), expected).map(({ reason }) => reason)).toEqual([
      'existence',
      'existence',
      'existence',
      'existence',
    ])
  })

  test.each(['', 'bad\0path'])('rejects invalid path %j without executing classification', async (path) => {
    await scoped(async (env) => {
      await expect(env.snapshot([path])).rejects.toBeInstanceOf(TypeError)
    })
    expect(backend.container.exec).not.toHaveBeenCalled()
  })

  test.each(['PermissionError: denied', 'ValueError: Unsupported filesystem kind', 'NotADirectoryError'])(
    'classification failure is not absence: %s',
    async (stderr) => {
      backend.container.exec.mockResolvedValue({
        exitCode: 1,
        stdout: JSON.stringify({ ok: false, error: stderr, observation: null }),
        stderr: '',
        output: '',
      })
      await driverScoped(async (env) => {
        await expect(env.snapshot(['/path'])).rejects.toThrow(stderr)
      })
    },
  )

  test.each(['null', '[]', '[{}]', '[{"kind":"socket"}]', '[{"kind":"symlink"}]'])(
    'rejects malformed classification %s',
    async (stdout) => {
      backend.container.exec.mockResolvedValue(driverResult(JSON.parse(stdout)))
      await driverScoped(async (env) => {
        await expect(env.snapshot(['/path'])).rejects.toThrow()
      })
    },
  )

  test('propagates regular-file read errors', async () => {
    backend.container.exec.mockResolvedValue({
      ...empty,
      exitCode: 1,
      stdout: JSON.stringify({ ok: false, error: 'file read denied', observation: null }),
      output: '',
    })
    await driverScoped(async (env) => {
      await expect(env.snapshot(['/file'])).rejects.toThrow('file read denied')
    })
  })

  test('checks without updating the baseline and reports sorted path reasons', async () => {
    backend.container.exec.mockResolvedValue(driverResult([{ path: '/path', kind: 'directory', data: [], target: [] }]))
    const baseline = new FilesystemSnapshot([{ path: '/path', state: { kind: 'missing' } }])
    await driverScoped(async (env) => {
      await expect(env.checkSnapshot(baseline)).rejects.toThrow(
        'Filesystem snapshot mismatch: [{"path":"/path","reason":"existence"}]',
      )
    })
    expect(baseline.entries).toEqual([{ path: '/path', state: { kind: 'missing' } }])
  })
})
