import { once } from 'node:events'
import { PassThrough, Readable } from 'node:stream'

import tar from 'tar-stream'
import * as testcontainers from 'testcontainers'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { DeadlineError, DockerError, installDriver, withTerminal, withEnvironment } from '../src/index.ts'
import type { FilesystemSnapshot, TerminalSession, Command, Environment } from '../src/index.ts'

const backend = vi.hoisted(() => {
  const container = {
    copyArchiveFromContainer: vi.fn<testcontainers.StartedTestContainer['copyArchiveFromContainer']>(),
    copyContentToContainer: vi.fn<testcontainers.StartedTestContainer['copyContentToContainer']>(),
    exec: vi.fn<testcontainers.StartedTestContainer['exec']>(),
    getId: () => 'case-container',
    stop: vi.fn<testcontainers.StartedTestContainer['stop']>(),
  }
  return { container, start: vi.fn<() => Promise<typeof container>>().mockResolvedValue(container) }
})

// Substitute only the external Testcontainers boundary, not Docker's wire protocol.
vi.mock('testcontainers', async (importOriginal) => {
  const actual = await importOriginal<typeof testcontainers>()
  return {
    ...actual,
    GenericContainer: class MockContainer {
      readonly start = backend.start
      readonly withLabels = vi.fn().mockReturnThis()
      readonly withPullPolicy = vi.fn().mockReturnThis()
      readonly withStartupTimeout = vi.fn().mockReturnThis()
    },
  }
})

const noop = async (): Promise<void> => {
  // The test supplies a callback that has no work.
}

const failedAction = async (reason: unknown): Promise<never> => {
  await noop()
  throw reason
}

const empty = { exitCode: 0, stderr: '', stdout: '' }
beforeEach(() => {
  backend.start.mockReset().mockResolvedValue(backend.container)
  backend.container.exec.mockReset().mockResolvedValue({ ...empty, output: '' })
  backend.container.copyContentToContainer.mockReset().mockResolvedValue()
  backend.container.copyArchiveFromContainer.mockReset()
  backend.container.stop.mockReset().mockResolvedValue({
    copyArchiveFromContainer: backend.container.copyArchiveFromContainer,
    getId: () => 'stopped',
  })
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

const scoped = async (action: (environment: Environment) => Promise<void>): Promise<void> => {
  await withEnvironment('caller:local', action)
}

const archive = (entries: { name: string; bytes?: Buffer; type?: 'file' | 'directory' | 'symlink' }[]): Readable => {
  const pack = tar.pack()
  for (const entry of entries) {
    pack.entry({ linkname: 'target', name: entry.name, type: entry.type ?? 'file' }, entry.bytes ?? Buffer.alloc(0))
  }
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
    backend.container.exec.mockResolvedValue({ exitCode: 7, output: 'outerr', stderr: 'err', stdout: 'out' })
    await scoped(async (env) => {
      await expect(env.run({ args: ['tool'] })).resolves.toEqual({ exitCode: 7, stderr: 'err', stdout: 'out' })
    })
  })
  test('preserves argv, empty arguments, working directory and environment values containing equals', async () => {
    await scoped(async (env) => {
      await env.run({ args: ['tool', '', 'a b'], env: ['A=two words', 'B=', 'C=a=b'], workingDir: '/work' })
    })
    expect(backend.container.exec).toHaveBeenCalledWith(['tool', '', 'a b'], {
      env: { A: 'two words', B: '', C: 'a=b' },
      workingDir: '/work',
    })
  })
  test('keeps special environment names and lets the last duplicate win', async () => {
    await scoped(async (env) => {
      await env.run({ args: ['tool'], env: ['__proto__=safe', 'constructor=value', 'A=old', 'A=new'] })
    })
    expect(backend.container.exec).toHaveBeenCalledWith(['tool'], {
      env: { A: 'new', ['__proto__']: 'safe', constructor: 'value' },
    })
  })
  test.each([-1, Number.NaN, 0.5])('rejects unavailable or invalid application exit code %j', async (exitCode) => {
    backend.container.exec.mockResolvedValue({ ...empty, exitCode, output: '' })
    await scoped(async (env) => {
      await expect(env.run({ args: ['tool'] })).rejects.toMatchObject({
        cause: { name: 'DockerError' },
        name: 'ExecutionError',
        partialResult: { stderr: '', stdout: '' },
      })
    })
  })
  test('infrastructure diagnostics never become child stderr or an application exit', async () => {
    const cause = Object.assign(new Error('daemon failed'), { statusCode: 500 })
    backend.container.exec.mockRejectedValue(cause)
    await scoped(async (env) => {
      await expect(env.run({ args: ['tool'] })).rejects.toMatchObject({
        cause: { cause, name: 'DockerError', statusCode: 500 },
        name: 'ExecutionError',
        partialResult: { stderr: '', stdout: '' },
      })
    })
  })
  test('returns unexpected stderr unchanged for framework assertions', async () => {
    backend.container.exec.mockResolvedValue({ ...empty, output: 'diagnostic', stderr: 'diagnostic' })
    await scoped(async (env) => {
      await expect(env.run({ args: ['tool'] })).resolves.toEqual({ ...empty, stderr: 'diagnostic' })
    })
  })
  test('returns the decoded text supplied by Testcontainers without trimming', async () => {
    const stdout = '\uFEFFhéllo\n'
    backend.container.exec.mockResolvedValue({ ...empty, output: stdout, stdout })
    await scoped(async (env) => {
      await expect(env.run({ args: ['tool'] })).resolves.toEqual({ ...empty, stdout })
    })
  })
})

describe('Scope lifecycle', () => {
  test('returns arbitrary callback results after cleanup and closes its signal', async () => {
    const value = { value: 42 }
    const callback: { signal?: AbortSignal } = {}
    await expect(
      withEnvironment('local', async (env) => {
        const { signal } = env
        callback.signal = signal
        return await Promise.resolve(value)
      }),
    ).resolves.toBe(value)
    expect(callback.signal?.aborted).toBe(true)
    expect(backend.container.stop).toHaveBeenCalledWith({ remove: true, removeVolumes: true, timeout: 0 })
  })
  test.each([new Error('assertion failure'), 'arbitrary failure', undefined])(
    'preserves callback rejection identity: %j',
    async (failure) => {
      await expect(withEnvironment('local', async () => await failedAction(failure))).rejects.toBe(failure)
      expect(backend.container.stop).toHaveBeenCalledOnce()
    },
  )
  test('retains callback and cleanup failures together', async () => {
    const failure = new Error('callback failed')
    const cleanup = new Error('cleanup failed')
    backend.container.stop.mockRejectedValue(cleanup)
    await expect(withEnvironment('local', async () => await failedAction(failure))).rejects.toMatchObject({
      errors: [failure, expect.objectContaining({ cause: cleanup, name: 'DockerError' })],
      name: 'AggregateError',
    })
  })
  test('reports cleanup failure alone', async () => {
    const cause = new Error('cleanup failed')
    backend.container.stop.mockRejectedValue(cause)
    await expect(scoped(noop)).rejects.toMatchObject({ cause, name: 'DockerError' })
  })
  test.each(['', 'image\0name'])('rejects invalid image %j before startup', async (image) => {
    await expect(withEnvironment(image, noop)).rejects.toBeInstanceOf(TypeError)
    expect(backend.start).not.toHaveBeenCalled()
  })
  test('a pre-cancelled invocation never starts Testcontainers', async () => {
    const reason = new Error('cancelled before setup')
    await expect(withEnvironment('local', noop, AbortSignal.abort(reason))).rejects.toBe(reason)
    expect(backend.start).not.toHaveBeenCalled()
  })
  test('startup failures retain their cause without invoking the callback', async () => {
    const cause = new Error('missing local image')
    backend.start.mockRejectedValue(cause)
    const action = vi.fn(noop)
    await expect(withEnvironment('missing', action)).rejects.toMatchObject({ cause, name: 'DockerError' })
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
          await noop()
        },
        controller.signal,
      ),
    ).rejects.toBe(reason)
    expect(backend.container.stop).toHaveBeenCalledOnce()
  })
  test('command timeout releases the scope even when Testcontainers exec never resolves', async () => {
    vi.useFakeTimers()
    backend.container.exec.mockReturnValue(Promise.withResolvers<never>().promise)
    const ready = Promise.withResolvers<true>()
    const execution = scoped(async (env) => {
      ready.resolve(true)
      await env.run({ args: ['sleep', 'infinity'] })
    })
    const assertion = expect(execution).rejects.toMatchObject({
      cause: { name: 'DeadlineError' },
      name: 'ExecutionError',
      partialResult: { stderr: '', stdout: '' },
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
    const action = vi.fn(noop)
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
    const diagnostic = vi.spyOn(testcontainers.log, 'error').mockImplementation(() => {
      // Capture the diagnostic without emitting it.
    })
    const assertion = expect(scoped(noop)).rejects.toBeInstanceOf(DeadlineError)
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
    backend.container.stop.mockReturnValue(Promise.withResolvers<never>().promise)
    const assertion = expect(scoped(noop)).rejects.toBeInstanceOf(DeadlineError)
    await vi.advanceTimersByTimeAsync(30_000)
    await assertion
  })
  test('does not add a fixed concurrency queue', async () => {
    const ready = Promise.withResolvers<true>()
    const release = Promise.withResolvers<true>()
    const entered: undefined[] = []
    const scopes = Array.from({ length: 3 }, async () => {
      await scoped(async () => {
        entered.push(undefined)
        if (entered.length === 3) {
          ready.resolve(true)
        }
        await release.promise
      })
    })
    await ready.promise
    expect(entered).toHaveLength(3)
    release.resolve(true)
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
      { content: bytes, mode: 0o644, target: '/workspace/binary' },
    ])
  })
  test('does not create missing parent directories', async () => {
    backend.container.exec.mockResolvedValue({ ...empty, exitCode: 1, output: '' })
    await scoped(async (env) => {
      await expect(env.writeFile('/missing/file', Buffer.alloc(0))).rejects.toThrow('Parent directory')
    })
    expect(backend.container.copyContentToContainer).not.toHaveBeenCalled()
  })
  test('reads exact bytes from the archive supplied by Testcontainers', async () => {
    const bytes = Buffer.from([0, 255, 10, 13, 1])
    backend.container.copyArchiveFromContainer.mockResolvedValue(archive([{ bytes, name: 'binary' }]))
    await scoped(async (env) => {
      await expect(env.readFile('/workspace/binary')).resolves.toEqual(bytes)
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
      await expect(env.readFile('/file')).rejects.toMatchObject({ cause, name: 'DockerError' })
    })
  })
  test('write transfer errors retain their original cause', async () => {
    const cause = new Error('copy failed')
    backend.container.copyContentToContainer.mockRejectedValue(cause)
    await scoped(async (env) => {
      await expect(env.writeFile('/file', Buffer.alloc(0))).rejects.toMatchObject({ cause, name: 'DockerError' })
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

describe('Physical path observations', () => {
  test.each(['pathKind', 'readSymlink'] as const)('%s rejects invalid paths before execution', async (method) => {
    await scoped(async (env) => {
      await Promise.all(
        ['', 'bad\0path'].map(async (path) => {
          await expect(env[method](path)).rejects.toBeInstanceOf(TypeError)
        }),
      )
    })
    expect(backend.container.exec).not.toHaveBeenCalled()
  })

  test.each([
    { exits: [0], kind: 'symlink' },
    { exits: [1, 0], kind: 'file' },
    { exits: [1, 1, 0], kind: 'directory' },
    { exits: [1, 1, 1, 1], kind: 'missing' },
    { exits: [1, 1, 1, 0], kind: 'other' },
  ])('classifies physical $kind without reading its content or target', async ({ exits, kind }) => {
    for (const exitCode of exits) {
      backend.container.exec.mockResolvedValueOnce({ ...empty, exitCode, output: '' })
    }
    await scoped(async (env) => {
      await expect(env.pathKind('/path')).resolves.toBe(kind)
    })
    expect(backend.container.exec).toHaveBeenCalledTimes(exits.length)
    expect(backend.container.copyArchiveFromContainer).not.toHaveBeenCalled()
  })

  test.each([0, 1, 2, 3])('probe failure at step %i is not absence', async (step) => {
    for (const _index of Array.from({ length: step })) {
      backend.container.exec.mockResolvedValueOnce({ ...empty, exitCode: 1, output: '' })
    }
    backend.container.exec.mockResolvedValueOnce({ ...empty, exitCode: 2, output: '', stderr: 'probe failed' })
    await scoped(async (env) => {
      await expect(env.pathKind('/path')).rejects.toBeInstanceOf(DockerError)
    })
  })

  test.each(['relative-target', '../target\n', '\uFEFFtarget\n\n'])(
    'reads literal decoded link target %j',
    async (target) => {
      backend.container.exec.mockResolvedValue({ ...empty, output: `${target}\n`, stdout: `${target}\n` })
      await scoped(async (env) => {
        await expect(env.readSymlink('/link')).resolves.toBe(target)
      })
    },
  )

  test.each([
    { ...empty, exitCode: 1 },
    { ...empty, stdout: 'no newline' },
    { ...empty, stderr: 'diagnostic', stdout: 'target\n' },
  ])('link read failure is not an empty target: %j', async (result) => {
    backend.container.exec.mockResolvedValue({ ...result, output: '' })
    await scoped(async (env) => {
      await expect(env.readSymlink('/link')).rejects.toBeInstanceOf(DockerError)
    })
  })

  test.each(['pathKind', 'readSymlink'] as const)('%s preserves infrastructure errors', async (method) => {
    const cause = new Error('daemon failed')
    backend.container.exec.mockRejectedValue(cause)
    await scoped(async (env) => {
      await expect(env[method]('/path')).rejects.toMatchObject({ cause: { cause }, name: 'ExecutionError' })
    })
  })
})

const installMock = async (env: Environment): Promise<void> => {
  backend.container.exec
    .mockResolvedValueOnce({ ...empty, output: '', stdout: '/tmp/caseyard-driver-test\n' })
    .mockResolvedValueOnce({ ...empty, output: '' })
  await installDriver(env, Buffer.from('executable'))
}

const driverScoped = async (action: (env: Environment) => Promise<void>): Promise<void> => {
  await scoped(async (env) => {
    await installMock(env)
    await action(env)
  })
}

const driverResult = (value: unknown) => ({ ...empty, output: '', stdout: JSON.stringify({ ok: true, value }) })

const terminalObservation = {
  currentTermios: [1, [2, 3]],
  initialTermios: [1, [2, 3]],
  status: { code: 7, kind: 'exited' },
  stdout: [0, 255, 27],
  terminal: [27, 91, 65],
  termiosRestored: true,
  unreadInput: [10],
}

const terminalBackend = (value: unknown = terminalObservation): void => {
  backend.container.exec.mockImplementation(async (args) => {
    if (args[1] === 'start') {
      return await Promise.resolve(driverResult({ path: '/tmp/caseyard-pty-test/socket' }))
    }
    const request: unknown = JSON.parse(args[2] ?? '{}')
    if (typeof request === 'object' && request !== null && 'operation' in request && request.operation === 'close') {
      return await Promise.resolve(driverResult(null))
    }
    return await Promise.resolve(driverResult(value))
  })
}

describe('Static driver installation', () => {
  test('concurrent install attempts cannot stage or overwrite a second driver', async () => {
    const staging = Promise.withResolvers<Awaited<ReturnType<testcontainers.StartedTestContainer['exec']>>>()
    backend.container.exec.mockReturnValueOnce(staging.promise)
    await scoped(async (env) => {
      const first = installDriver(env, Buffer.from('first'))
      await expect(installDriver(env, Buffer.from('second'))).rejects.toThrow('already installed')
      staging.resolve({ ...empty, output: '', stdout: '/tmp/caseyard-driver-test\n' })
      await first
    })
    expect(backend.container.copyContentToContainer).toHaveBeenCalledExactlyOnceWith([
      { content: Buffer.from('first'), mode: 0o755, target: '/tmp/caseyard-driver-test/driver' },
    ])
  })

  test('failed installation does not publish a usable driver', async () => {
    backend.container.exec.mockResolvedValue({ ...empty, exitCode: 1, output: '', stderr: 'mktemp denied' })
    await scoped(async (env) => {
      await expect(installDriver(env, Buffer.from('executable'))).rejects.toThrow('mktemp denied')
      await expect(env.snapshot(['/path'])).rejects.toThrow('installDriver')
    })
    expect(backend.container.copyContentToContainer).not.toHaveBeenCalled()
  })

  test('driver APIs reject use before installation', async () => {
    await scoped(async (env) => {
      await expect(env.snapshot(['/file'])).rejects.toThrow('installDriver')
      await expect(withTerminal(env, { args: ['tool'] }, {}, noop)).rejects.toThrow('installDriver')
    })
    expect(backend.container.exec).not.toHaveBeenCalled()
  })

  test('stages exact bytes privately, makes them executable, and rejects a second install', async () => {
    await driverScoped(async (env) => {
      await expect(installDriver(env, Buffer.from('other'))).rejects.toThrow('already installed')
    })
    expect(backend.container.copyContentToContainer).toHaveBeenCalledWith([
      { content: Buffer.from('executable'), mode: 0o755, target: '/tmp/caseyard-driver-test/driver' },
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
    const closing = Promise.withResolvers<true>()
    const closed = Promise.withResolvers<Awaited<ReturnType<testcontainers.StartedTestContainer['exec']>>>()
    const execution = withEnvironment(
      'local',
      async (env) => {
        await installMock(env)
        await withTerminal(env, { args: ['tool'] }, {}, async () => {
          backend.container.exec.mockImplementationOnce(async () => {
            closing.resolve(true)
            return await closed.promise
          })
          await noop()
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
    const closing = Promise.withResolvers<true>()
    const execution = scoped(async (env) => {
      await installMock(env)
      await withTerminal(env, { args: ['tool'] }, {}, async () => {
        await vi.advanceTimersByTimeAsync(59_999)
        backend.container.exec.mockImplementationOnce(async () => {
          closing.resolve(true)
          return await Promise.withResolvers<never>().promise
        })
      })
    })
    const assertion = expect(execution).rejects.toMatchObject({
      cause: { message: 'Terminal cleanup deadline exceeded' },
      name: 'ExecutionError',
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
    const handle: { session?: TerminalSession } = {}
    await expect(
      withEnvironment(
        'local',
        async (env) => {
          await installMock(env)
          await withTerminal(env, { args: ['tool'] }, {}, async (session) => {
            handle.session = session
            backend.container.exec.mockReturnValueOnce(Promise.withResolvers<never>().promise)
            const assertion = expect(session.observe()).rejects.toMatchObject({ cause: reason, name: 'ExecutionError' })
            controller.abort(reason)
            await assertion
          })
        },
        controller.signal,
      ),
    ).rejects.toBe(reason)
    if (!handle.session) {
      throw new Error('Expected a terminal session')
    }
    await expect(handle.session.observe()).rejects.toThrow('closed')
    expect(backend.container.stop).toHaveBeenCalledOnce()
    expect(backend.container.exec.mock.calls.some(([args]) => args[2]?.includes('"operation":"close"') === true)).toBe(
      false,
    )
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
    const handle: { session?: TerminalSession } = {}
    await driverScoped(async (env) => {
      await expect(
        withTerminal(env, { args: ['tool', ''], env: ['A=b=c'], workingDir: '/workspace' }, {}, async (session) => {
          handle.session = session
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
      if (!handle.session) {
        throw new Error('Expected a terminal session')
      }
      await expect(handle.session.observe()).rejects.toThrow('closed')
    })
    expect(backend.container.exec).toHaveBeenCalledWith(
      [
        '/tmp/caseyard-driver-test/driver',
        'start',
        JSON.stringify({ argv: ['tool', ''], columns: 120, input: null, rows: 40, timeout: 50 }),
      ],
      { env: { A: 'b=c' }, workingDir: '/workspace' },
    )
    expect(backend.container.exec).toHaveBeenLastCalledWith(
      [
        '/tmp/caseyard-driver-test/driver',
        'request',
        JSON.stringify({ operation: 'close', path: '/tmp/caseyard-pty-test/socket' }),
      ],
      { env: {} },
    )
  })

  test.each([new Error('callback failure'), undefined])(
    'closes after arbitrary callback rejection %j',
    async (cause) => {
      terminalBackend()
      await driverScoped(async (env) => {
        await expect(withTerminal(env, { args: ['tool'] }, {}, async () => await failedAction(cause))).rejects.toBe(
          cause,
        )
      })
      expect(backend.container.exec).toHaveBeenLastCalledWith(
        expect.arrayContaining([expect.stringContaining('"operation":"close"')]),
        { env: {} },
      )
    },
  )

  test('retains callback and close failures together', async () => {
    backend.container.exec.mockImplementation(
      async (args) =>
        await Promise.resolve(
          args[1] === 'start'
            ? driverResult({ path: '/tmp/caseyard-pty-test/socket' })
            : {
                ...empty,
                exitCode: 1,
                output: '',
                stdout: JSON.stringify({ error: 'close failed', observation: null, ok: false }),
              },
        ),
    )
    const cause = new Error('callback failure')
    await driverScoped(async (env) => {
      await expect(
        withTerminal(env, { args: ['tool'] }, {}, async () => await failedAction(cause)),
      ).rejects.toMatchObject({
        errors: [cause, expect.objectContaining({ message: 'close failed', name: 'TerminalError' })],
        name: 'AggregateError',
      })
    })
  })

  test('pipe stdin keeps unread bytes and rejects interactive sends', async () => {
    terminalBackend()
    await driverScoped(async (env) => {
      await withTerminal(env, { args: ['tool'] }, { input: Buffer.from([0, 255]) }, async (session) => {
        await expect(session.send(Buffer.from('x'))).rejects.toThrow('pipe')
        const terminalObservationResult = await session.wait()
        expect(terminalObservationResult.unreadInput).toEqual(Buffer.from([10]))
      })
    })
  })

  test.each([
    { rows: 0 },
    { columns: 65_536 },
    { rows: 0.5 },
    { timeout: 0 },
    { timeout: Number.NaN },
    { timeout: 61 },
  ])('invalid terminal options fail before start %j', async (options) => {
    await scoped(async (env) => {
      await expect(withTerminal(env, { args: ['tool'] }, options, noop)).rejects.toBeInstanceOf(TypeError)
    })
    expect(backend.container.exec).not.toHaveBeenCalled()
  })

  test.each([
    { ...terminalObservation, stdout: [256] },
    { ...terminalObservation, terminal: [-1] },
    { ...terminalObservation, unreadInput: [1.5] },
    { ...terminalObservation, status: { code: -1, kind: 'exited' } },
    { ...terminalObservation, status: { code: 7, kind: 'running' } },
    { ...terminalObservation, status: { code: 7, kind: 'exited', signal: 15 } },
    { ...terminalObservation, status: { code: 7, kind: 'signaled', signal: 15 } },
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
          output: '',
          stdout: JSON.stringify({
            error: 'waiting for terminal bytes',
            observation: terminalObservation,
            ok: false,
          }),
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
  test('defensively copies arbitrary link target bytes on observation', async () => {
    backend.container.exec.mockResolvedValue(
      driverResult([{ data: [], kind: 'symlink', path: '/link', target: [255, 10] }]),
    )
    await driverScoped(async (env) => {
      const snapshot = await env.snapshot(['/link'])
      const [entry] = snapshot.entries
      if (!entry) {
        throw new Error('Expected one snapshot entry')
      }
      const { state } = entry
      if (state.kind !== 'symlink') {
        throw new Error('Expected symlink')
      }
      state.target.fill(1)
      expect(snapshot).toEqual({
        entries: [{ path: '/link', state: { kind: 'symlink', target: Buffer.from([255, 10]) } }],
      })
    })
  })

  test('empty explicit path sets do not execute or require driver installation', async () => {
    await scoped(async (env) => {
      const observedSnapshot = await env.snapshot([])
      expect(observedSnapshot.entries).toEqual([])
    })
    expect(backend.container.exec).not.toHaveBeenCalled()
  })

  test.each([
    { data: [], kind: 'socket', path: '/path', target: [] },
    { data: [256], kind: 'file', path: '/path', target: [] },
    { data: [], kind: 'symlink', path: '/path', target: [-1] },
    { data: [1], kind: 'directory', path: '/path', target: [] },
    { data: [], kind: 'missing', path: '/other', target: [] },
  ])('rejects invalid snapshot wire entry %j', async (entry) => {
    backend.container.exec.mockResolvedValue(driverResult([entry]))
    await driverScoped(async (env) => {
      await expect(env.snapshot(['/path'])).rejects.toThrow()
    })
  })

  test('literal targets preserve invalid UTF-8, byte-order markers and trailing newlines', async () => {
    const target = Buffer.concat([Buffer.from([255]), Buffer.from('\uFEFFtarget\n')])
    backend.container.exec.mockResolvedValue(
      driverResult([{ data: [], kind: 'symlink', path: '/link', target: [...Buffer.from(target)] }]),
    )
    await driverScoped(async (env) => {
      const observedSnapshot = await env.snapshot(['/link'])
      expect(observedSnapshot.entries).toEqual([{ path: '/link', state: { kind: 'symlink', target } }])
    })
  })

  test('captures sorted explicit paths, exact bytes, directories and literal dangling links', async () => {
    const bytes = Buffer.from([0, 255, 13, 10])
    backend.container.exec.mockResolvedValue(
      driverResult([
        { data: [], kind: 'directory', path: '/dir', target: [] },
        { data: [...bytes], kind: 'file', path: '/file', target: [] },
        { data: [], kind: 'symlink', path: '/link', target: [...Buffer.from('../absent\n')] },
        { data: [], kind: 'missing', path: '/missing', target: [] },
      ]),
    )
    await driverScoped(async (env) => {
      const observedSnapshot = await env.snapshot(['/missing', '/link', '/file', '/dir', '/file'])
      expect(observedSnapshot.entries).toEqual([
        { path: '/dir', state: { kind: 'directory' } },
        { path: '/file', state: { bytes, kind: 'file' } },
        { path: '/link', state: { kind: 'symlink', target: Buffer.from('../absent\n') } },
        { path: '/missing', state: { kind: 'missing' } },
      ])
    })
    expect(backend.container.copyArchiveFromContainer).not.toHaveBeenCalled()
  })

  test('detaches captured byte buffers on every read', async () => {
    backend.container.exec.mockResolvedValue(
      driverResult([{ data: [0, 255], kind: 'file', path: '/file', target: [] }]),
    )
    await driverScoped(async (env) => {
      const snapshot = await env.snapshot(['/file'])
      const [entry] = snapshot.entries
      if (!entry) {
        throw new Error('Expected one snapshot entry')
      }
      const { state } = entry
      if (state.kind !== 'file') {
        throw new Error('Expected file')
      }
      state.bytes.fill(2)
      expect(snapshot).toEqual({ entries: [{ path: '/file', state: { bytes: Buffer.from([0, 255]), kind: 'file' } }] })
      expect(Object.isFrozen(snapshot.entries)).toBe(true)
      expect(Object.isFrozen(snapshot)).toBe(true)
    })
  })

  test('exposes sorted plain state for framework equality and saved snapshots', async () => {
    backend.container.exec.mockResolvedValue(
      driverResult([
        { data: [0, 255, 13, 10], kind: 'file', path: '/content', target: [] },
        { data: [], kind: 'directory', path: '/directory', target: [] },
        { data: [], kind: 'missing', path: '/missing', target: [] },
        { data: [], kind: 'symlink', path: '/target', target: [255, 10] },
      ]),
    )
    await driverScoped(async (env) => {
      const snapshot = await env.snapshot(['/target', '/missing', '/content', '/directory'])
      expect(snapshot).toMatchSnapshot()
      expect(snapshot).toEqual({
        entries: [
          { path: '/content', state: { bytes: Buffer.from([0, 255, 13, 10]), kind: 'file' } },
          { path: '/directory', state: { kind: 'directory' } },
          { path: '/missing', state: { kind: 'missing' } },
          { path: '/target', state: { kind: 'symlink', target: Buffer.from([255, 10]) } },
        ],
      })
    })
  })

  test.each([
    { path: '/different', state: { bytes: Buffer.from([0, 255]), kind: 'file' as const } },
    { path: '/file', state: { bytes: Buffer.from([0, 254]), kind: 'file' as const } },
    { path: '/file', state: { kind: 'missing' as const } },
    { path: '/file', state: { kind: 'directory' as const } },
    { path: '/file', state: { kind: 'symlink' as const, target: Buffer.from([0, 255]) } },
  ])('native equality sees changed path, bytes, presence or kind: %j', async (entry) => {
    backend.container.exec.mockResolvedValue(
      driverResult([{ data: [0, 255], kind: 'file', path: '/file', target: [] }]),
    )
    await driverScoped(async (env) => {
      expect(await env.snapshot(['/file'])).not.toEqual({ entries: [entry] })
      expect(await env.snapshot(['/file'])).not.toEqual({ entries: [] })
    })
  })

  test('native equality sees changed literal link target bytes', async () => {
    backend.container.exec.mockResolvedValue(
      driverResult([{ data: [], kind: 'symlink', path: '/link', target: [255] }]),
    )
    await driverScoped(async (env) => {
      expect(await env.snapshot(['/link'])).not.toEqual({
        entries: [{ path: '/link', state: { kind: 'symlink', target: Buffer.from([254]) } }],
      })
    })
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
        output: '',
        stderr: '',
        stdout: JSON.stringify({ error: stderr, observation: null, ok: false }),
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
      output: '',
      stdout: JSON.stringify({ error: 'file read denied', observation: null, ok: false }),
    })
    await driverScoped(async (env) => {
      await expect(env.snapshot(['/file'])).rejects.toThrow('file read denied')
    })
  })

  test('recapture returns changed state without changing the baseline', async () => {
    backend.container.exec.mockResolvedValue(driverResult([{ data: [], kind: 'directory', path: '/path', target: [] }]))
    const baseline: FilesystemSnapshot = { entries: [{ path: '/path', state: { kind: 'missing' } }] }
    await driverScoped(async (env) => {
      const observedSnapshot = await env.snapshot(baseline.entries.map(({ path }) => path))
      expect(observedSnapshot.entries).toEqual([{ path: '/path', state: { kind: 'directory' } }])
    })
    expect(baseline.entries).toEqual([{ path: '/path', state: { kind: 'missing' } }])
  })
})
