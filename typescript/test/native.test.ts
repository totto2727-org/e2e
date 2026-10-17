import { once } from 'node:events'
import { PassThrough, Readable } from 'node:stream'

import tar from 'tar-stream'
import { log, type StartedTestContainer } from 'testcontainers'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { DeadlineError, DockerError, withEnvironment, type Command, type Environment } from '../src/index.js'

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
