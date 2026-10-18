import { execFile } from 'node:child_process'
import { readFile } from 'node:fs/promises'
import { promisify } from 'node:util'

import { afterAll, beforeAll, expect, test } from 'vitest'

import {
  DeadlineError,
  compareSnapshots,
  installDriver,
  withTerminal,
  type TerminalSession,
  withEnvironment,
  type Environment,
} from '../src/index.js'

const image = 'e2e-example:local'
const execute = promisify(execFile)
async function docker(...args: string[]): Promise<string> {
  return (await execute('docker', args, { timeout: 30_000 })).stdout.trim()
}
const identities: string[] = []
let imageID = ''
let baseline: string[] = []

async function containers(): Promise<string[]> {
  const output = await docker('ps', '-aq', '--no-trunc', '--filter', 'label=org.totto2727.e2e.owner')
  return output ? output.split('\n').sort() : []
}
beforeAll(async () => {
  baseline = await containers()
  imageID = await docker('image', 'inspect', '--format', '{{.Id}}', image)
}, 30_000)
afterAll(async () => {
  expect(identities.length).toBeGreaterThan(0)
  expect(new Set(identities).size).toBe(identities.length)
  expect(await containers()).toEqual(baseline)
  expect(await docker('image', 'inspect', '--format', '{{.Id}}', image)).toBe(imageID)
}, 30_000)

async function checkIdentity(env: Environment, expectedImageID = imageID): Promise<void> {
  const hostname = await env.run({ args: ['hostname'] })
  expect(hostname.exitCode).toBe(0)
  expect(hostname.stderr).toBe('')
  const id = hostname.stdout.trim()
  expect(id).toMatch(/^[a-f0-9]{12}$/)
  identities.push(id)
  expect(await docker('inspect', '--format', '{{.Image}}', id)).toBe(expectedImageID)
}

async function installFixtureDriver(env: Environment): Promise<void> {
  const machine = await env.run({ args: ['uname', '-m'] })
  expect(machine.exitCode).toBe(0)
  const architecture = machine.stdout.trim() === 'aarch64' ? 'arm64' : 'amd64'
  expect(['aarch64', 'x86_64']).toContain(machine.stdout.trim())
  await installDriver(env, await readFile(new URL(`../../go/target/driver-linux-${architecture}`, import.meta.url)))
}

const empty = { exitCode: 0, stdout: '', stderr: '' }

test.concurrent('greeting', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      expect(await env.run({ args: ['echo', 'hello from e2e'] })).toEqual({
        exitCode: 0,
        stdout: 'hello from e2e\n',
        stderr: '',
      })
    },
    context.signal,
  )
})

test.concurrent('file workflow', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await env.checkCommand({ args: ['sh', '-c', 'echo first > /workspace/result.txt'] }, empty)
      await env.checkCommand({ args: ['sh', '-c', 'echo second >> /workspace/result.txt'] }, empty)
      await env.checkCommand({ args: ['cat', '/workspace/result.txt'] }, { ...empty, stdout: 'first\nsecond\n' })
      await env.checkFile('/workspace/result.txt', Buffer.from('first\nsecond\n'))
      const bytes = Buffer.from([0, 255, 10, 13, 1])
      await env.writeFile('/workspace/binary', bytes)
      expect(await env.readFile('/workspace/binary')).toEqual(bytes)
      await env.checkCommand({ args: ['stat', '-c', '%a', '/workspace/binary'] }, { ...empty, stdout: '644\n' })
    },
    context.signal,
  )
})

test.concurrent('expected failure', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await env.checkCommand(
        { args: ['sh', '-c', 'echo expected-failure; exit 7'] },
        { exitCode: 7, stdout: 'expected-failure\n', stderr: '' },
      )
    },
    context.signal,
  )
})

test.concurrent('separate streams', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await env.checkCommand(
        {
          args: ['sh', '-c', 'printf "%s:%s\\n" "$HOME" "$PWD"; printf "diagnostic\\n" >&2; exit 7'],
          workingDir: '/workspace',
          env: ['HOME=/workspace/synthetic-home'],
        },
        { exitCode: 7, stdout: '/workspace/synthetic-home:/workspace\n', stderr: 'diagnostic\n' },
      )
    },
    context.signal,
  )
})

test.concurrent('physical paths', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await env.writeFile('/workspace/result.txt', Buffer.from('fixture\n'))
      for (const args of [
        ['mkdir', '/workspace/directory'],
        ['ln', '-s', 'result.txt', '/workspace/link'],
        ['ln', '-s', 'missing.txt', '/workspace/dangling'],
        ['ln', '-s', 'directory', '/workspace/directory-link'],
      ])
        await env.checkCommand({ args }, empty)
      await env.checkRegularFile('/workspace/result.txt')
      await env.checkDirectory('/workspace/directory')
      await env.checkMissing('/workspace/missing.txt')
      await env.checkSymlink('/workspace/link', 'result.txt')
      await env.checkSymlink('/workspace/dangling', 'missing.txt')
      await expect(env.checkMissing('/workspace/dangling')).rejects.toThrow()
      await expect(env.checkMissing('/workspace/result.txt')).rejects.toThrow()
      await expect(env.checkRegularFile('/workspace/link')).rejects.toThrow()
      await expect(env.checkDirectory('/workspace/directory-link')).rejects.toThrow()
      await expect(env.checkSymlink('/workspace/link', '/workspace/result.txt')).rejects.toThrow()
    },
    context.signal,
  )
})

test.concurrent('deadline', async (context) => {
  const started = Date.now()
  await expect(
    withEnvironment(
      image,
      async (env) => {
        await checkIdentity(env)
        await expect(env.run({ args: ['sleep', 'infinity'] })).rejects.toMatchObject({
          name: 'ExecutionError',
          cause: new DeadlineError(),
          partialResult: { stdout: '', stderr: '' },
        })
        expect(Date.now() - started).toBeLessThan(70_000)
      },
      context.signal,
    ),
  ).rejects.toBeInstanceOf(DeadlineError)
})

test.concurrent('filesystem snapshots preserve physical kinds and diagnose sorted changes', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      await env.writeFile('/workspace/binary', Buffer.from([0, 255, 10, 13]))
      await env.checkCommand(
        {
          args: [
            'sh',
            '-c',
            'mkdir /workspace/directory; ln -s binary /workspace/link; ln -s missing /workspace/dangling',
          ],
        },
        empty,
      )
      const paths = [
        '/workspace/missing',
        '/workspace/link',
        '/workspace/binary',
        '/workspace/directory',
        '/workspace/dangling',
      ]
      const before = await env.snapshot(paths)
      expect(before.entries).toEqual([
        { path: '/workspace/binary', state: { kind: 'file', bytes: Buffer.from([0, 255, 10, 13]) } },
        { path: '/workspace/dangling', state: { kind: 'symlink', target: Buffer.from('missing') } },
        { path: '/workspace/directory', state: { kind: 'directory' } },
        { path: '/workspace/link', state: { kind: 'symlink', target: Buffer.from('binary') } },
        { path: '/workspace/missing', state: { kind: 'missing' } },
      ])
      await env.checkSnapshot(before)
      await env.writeFile('/workspace/directory/ignored', Buffer.from('no recursion'))
      await env.checkSnapshot(before)
      await env.writeFile('/workspace/binary', Buffer.from([0, 254, 10, 13]))
      await env.checkCommand(
        {
          args: [
            'sh',
            '-c',
            'rm /workspace/link; ln -s ./binary /workspace/link; rmdir /workspace/directory 2>/dev/null || rm -r /workspace/directory; touch /workspace/directory; mkdir /workspace/missing',
          ],
        },
        empty,
      )
      expect(compareSnapshots(before, await env.snapshot(paths))).toEqual([
        { path: '/workspace/binary', reason: 'content' },
        { path: '/workspace/directory', reason: 'kind' },
        { path: '/workspace/link', reason: 'target' },
        { path: '/workspace/missing', reason: 'existence' },
      ])
      await expect(env.checkSnapshot(before)).rejects.toThrow('Filesystem snapshot mismatch')
      expect(before.entries[0]).toEqual({
        path: '/workspace/binary',
        state: { kind: 'file', bytes: Buffer.from([0, 255, 10, 13]) },
      })
    },
    context.signal,
  )
})

test.concurrent('filesystem snapshots reject special files and non-directory path errors', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      await env.checkCommand({ args: ['mkfifo', '/workspace/fifo'] }, empty)
      await expect(env.snapshot(['/workspace/fifo'])).rejects.toThrow(/unsupported/i)
      await env.writeFile('/workspace/file', Buffer.from('file'))
      await expect(env.snapshot(['/workspace/file/child'])).rejects.toThrow(/not a directory/i)
    },
    context.signal,
  )
})

test.concurrent('filesystem snapshots preserve arbitrary literal link target bytes', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      await env.checkCommand({ args: ['sh', '-c', 'ln -s "$(printf "\\377")" /workspace/raw-link'] }, empty)
      expect((await env.snapshot(['/workspace/raw-link'])).entries).toEqual([
        { path: '/workspace/raw-link', state: { kind: 'symlink', target: Buffer.from([255]) } },
      ])
    },
    context.signal,
  )
})

test.concurrent('terminal keeps raw stdout separate and observes restored termios after input', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      const command = {
        args: [
          'sh',
          '-c',
          `saved=$(stty -g < /dev/tty); stty -echo -icanon -isig -opost min 1 time 0 < /dev/tty; printf '\\033[?1049hready' >&2; dd bs=1 count=1 2>/dev/null | od -An -tu1; printf '\\000\\377'; stty "$saved" < /dev/tty; printf '\\033[?1049l' >&2; exit 7`,
        ],
      }
      await withTerminal(env, command, { rows: 20, columns: 80 }, async (session) => {
        const ready = await session.expect('terminal', Buffer.from('\x1b[?1049hready'))
        expect(ready.status).toEqual({ kind: 'running' })
        expect(ready.termiosRestored).toBe(false)
        await session.send(Buffer.from([3]))
        const done = await session.wait()
        expect(done.stdout).toEqual(Buffer.concat([Buffer.from('   3\n'), Buffer.from([0, 255])]))
        expect(done.terminal).toEqual(Buffer.from('\x1b[?1049hready\x1b[?1049l'))
        expect(done.status).toEqual({ kind: 'exited', code: 7 })
        expect(done.termiosRestored).toBe(true)
        expect(done.currentTermios).toEqual(done.initialTermios)
      })
    },
    context.signal,
  )
})

test.concurrent('terminal finite pipe input reports unread bytes and application failure', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      const input = Buffer.from([0, 255, 13, 10])
      await withTerminal(
        env,
        { args: ['sh', '-c', 'printf "\\377"; printf "ready" >&2; exit 7'] },
        { input },
        async (session) => {
          const done = await session.wait()
          expect(done.stdout).toEqual(Buffer.from([255]))
          expect(done.terminal).toEqual(Buffer.from('ready'))
          expect(done.unreadInput).toEqual(input)
          expect(done.status).toEqual({ kind: 'exited', code: 7 })
          expect(done.termiosRestored).toBe(true)
          await expect(session.send(Buffer.from('x'))).rejects.toThrow('pipe')
        },
      )
    },
    context.signal,
  )
})

test.concurrent('non-root images install executable drivers without changing ordinary file modes', async (context) => {
  const nonrootImage = 'e2e-example-nonroot:local'
  const original = await docker('image', 'inspect', '--format', '{{.Id}}', nonrootImage)
  await withEnvironment(
    nonrootImage,
    async (env) => {
      await checkIdentity(env, original)
      expect((await env.run({ args: ['id', '-u'] })).stdout.trim()).not.toBe('0')
      await installFixtureDriver(env)
      await env.writeFile('/tmp/nonroot-file', Buffer.from([0, 255]))
      await env.checkCommand({ args: ['stat', '-c', '%a', '/tmp/nonroot-file'] }, { ...empty, stdout: '644\n' })
      expect((await env.snapshot(['/tmp/nonroot-file'])).entries).toEqual([
        { path: '/tmp/nonroot-file', state: { kind: 'file', bytes: Buffer.from([0, 255]) } },
      ])
      await withTerminal(env, { args: ['sh', '-c', 'printf "\\377"; printf nonroot >&2'] }, {}, async (terminal) => {
        const result = await terminal.wait()
        expect(result.stdout).toEqual(Buffer.from([255]))
        expect(result.terminal).toEqual(Buffer.from('nonroot'))
        expect(result.status).toEqual({ kind: 'exited', code: 0 })
      })
    },
    context.signal,
  )
  expect(await docker('image', 'inspect', '--format', '{{.Id}}', nonrootImage)).toBe(original)
})

test.concurrent('terminal resize reaches the foreground process and retains trailing matches', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      await withTerminal(
        env,
        {
          args: [
            'sh',
            '-c',
            `trap 'stty size < /dev/tty >&2; exit 0' WINCH; printf 'ready:tail' >&2; while :; do sleep 1; done`,
          ],
        },
        {},
        async (session) => {
          await session.expect('terminal', Buffer.from('ready:'))
          await session.expect('terminal', Buffer.from('tail'))
          await session.resize(12, 34)
          await session.expect('terminal', Buffer.from('12 34\r\n'))
          expect((await session.wait()).status).toEqual({ kind: 'exited', code: 0 })
        },
      )
    },
    context.signal,
  )
})

test.concurrent('terminal expectation timeout retains diagnostics and literal Ctrl-D supplies EOF', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      await withTerminal(env, { args: ['sh', '-c', 'printf ready >&2; cat'] }, {}, async (session) => {
        await session.expect('terminal', Buffer.from('ready'))
        await expect(session.expect('stdout', Buffer.from('missing'), 0.1)).rejects.toMatchObject({
          name: 'TerminalError',
          observation: { stdout: Buffer.alloc(0), terminal: Buffer.from('ready'), status: { kind: 'running' } },
        })
        await env.checkDirectory('/workspace')
        await session.send(Buffer.from([4]))
        const result = await session.wait()
        expect(result.status).toEqual({ kind: 'exited', code: 0 })
        expect(result.stdout).toEqual(Buffer.alloc(0))
        expect(result.termiosRestored).toBe(true)
      })
    },
    context.signal,
  )
})

test.concurrent('terminal signals and callback failure clean up a surviving process and invalidate handles', async (context) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      await withTerminal(env, { args: ['sh', '-c', 'printf ready >&2; exec sleep infinity'] }, {}, async (session) => {
        await session.expect('terminal', Buffer.from('ready'))
        await session.signal('SIGTERM')
        expect((await session.wait()).status).toEqual({ kind: 'signaled', signal: 15 })
      })
      let escaped!: TerminalSession
      const cause = new Error('consumer assertion failed')
      await expect(
        withTerminal(
          env,
          { args: ['sh', '-c', 'echo $$ > /workspace/terminal-pid; printf ready >&2; exec sleep infinity'] },
          {},
          async (session) => {
            escaped = session
            await session.expect('terminal', Buffer.from('ready'))
            throw cause
          },
        ),
      ).rejects.toBe(cause)
      await expect(escaped.observe()).rejects.toThrow('closed')
      expect((await env.run({ args: ['sh', '-c', 'kill -0 "$(cat /workspace/terminal-pid)"'] })).exitCode).not.toBe(0)
      await env.checkCommand({ args: ['sh', '-c', 'find /tmp -maxdepth 1 -name "caseyard-pty-*" -print'] }, empty)
    },
    context.signal,
  )
})
