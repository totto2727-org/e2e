import { execFile } from 'node:child_process'
import { readFile } from 'node:fs/promises'
import { promisify } from 'node:util'

import { afterAll, beforeAll, expect, test } from 'vitest'

import {
  DeadlineError,
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

test.concurrent('greeting', async ({ expect, signal }) => {
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
    signal,
  )
})

test.concurrent('file workflow', async ({ expect, signal }) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      expect(await env.run({ args: ['sh', '-c', 'echo first > /workspace/result.txt'] })).toEqual(empty)
      expect(await env.run({ args: ['sh', '-c', 'echo second >> /workspace/result.txt'] })).toEqual(empty)
      expect(await env.run({ args: ['cat', '/workspace/result.txt'] })).toEqual({ ...empty, stdout: 'first\nsecond\n' })
      expect(await env.readFile('/workspace/result.txt')).toEqual(Buffer.from('first\nsecond\n'))
      const bytes = Buffer.from([0, 255, 10, 13, 1])
      await env.writeFile('/workspace/binary', bytes)
      expect(await env.readFile('/workspace/binary')).toEqual(bytes)
      expect(await env.run({ args: ['stat', '-c', '%a', '/workspace/binary'] })).toEqual({ ...empty, stdout: '644\n' })
    },
    signal,
  )
})

test.concurrent('expected failure', async ({ expect, signal }) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      expect(await env.run({ args: ['sh', '-c', 'echo expected-failure; exit 7'] })).toEqual({
        exitCode: 7,
        stdout: 'expected-failure\n',
        stderr: '',
      })
    },
    signal,
  )
})

test.concurrent('separate streams', async ({ expect, signal }) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      expect(
        await env.run({
          args: ['sh', '-c', 'printf "%s:%s\\n" "$HOME" "$PWD"; printf "diagnostic\\n" >&2; exit 7'],
          workingDir: '/workspace',
          env: ['HOME=/workspace/synthetic-home'],
        }),
      ).toEqual({ exitCode: 7, stdout: '/workspace/synthetic-home:/workspace\n', stderr: 'diagnostic\n' })
    },
    signal,
  )
})

test.concurrent('physical paths', async ({ expect, signal }) => {
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
        expect(await env.run({ args })).toEqual(empty)
      expect(await env.pathKind('/workspace/result.txt')).toBe('file')
      expect(await env.pathKind('/workspace/directory')).toBe('directory')
      expect(await env.pathKind('/workspace/missing.txt')).toBe('missing')
      expect(await env.pathKind('/workspace/link')).toBe('symlink')
      expect(await env.pathKind('/workspace/dangling')).toBe('symlink')
      expect(await env.pathKind('/workspace/directory-link')).toBe('symlink')
      expect(await env.readSymlink('/workspace/link')).toBe('result.txt')
      expect(await env.readSymlink('/workspace/dangling')).toBe('missing.txt')
      expect(await env.run({ args: ['mkfifo', '/workspace/fifo'] })).toEqual(empty)
      expect(await env.pathKind('/workspace/fifo')).toBe('other')
      await expect(env.readSymlink('/workspace/result.txt')).rejects.toThrow()
    },
    signal,
  )
})

test.concurrent('deadline', async ({ expect, signal }) => {
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
      signal,
    ),
  ).rejects.toBeInstanceOf(DeadlineError)
})

test.concurrent('filesystem snapshots preserve physical kinds and save sorted changes', async ({ expect, signal }) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      await env.writeFile('/workspace/binary', Buffer.from([0, 255, 10, 13]))
      expect(
        await env.run({
          args: [
            'sh',
            '-c',
            'mkdir /workspace/directory; ln -s binary /workspace/link; ln -s missing /workspace/dangling',
          ],
        }),
      ).toEqual(empty)
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
      expect(before).toMatchSnapshot('initial physical state')
      expect(await env.snapshot(paths)).toEqual(before)
      await env.writeFile('/workspace/directory/ignored', Buffer.from('no recursion'))
      expect(await env.snapshot(paths)).toEqual(before)
      await env.writeFile('/workspace/binary', Buffer.from([0, 254, 10, 13]))
      expect(
        await env.run({
          args: [
            'sh',
            '-c',
            'rm /workspace/link; ln -s ./binary /workspace/link; rmdir /workspace/directory 2>/dev/null || rm -r /workspace/directory; touch /workspace/directory; mkdir /workspace/missing',
          ],
        }),
      ).toEqual(empty)
      expect(await env.snapshot(paths)).toMatchSnapshot('changed physical state')
      expect(before.entries[0]).toEqual({
        path: '/workspace/binary',
        state: { kind: 'file', bytes: Buffer.from([0, 255, 10, 13]) },
      })
    },
    signal,
  )
})

test.concurrent('filesystem snapshots detect JSON whitespace bytes and deletion at whitespace paths', async ({
  expect,
  signal,
}) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      const path = '/workspace/config file.json'
      await env.writeFile(path, Buffer.from('{"value":1}\n'))
      const before = await env.snapshot([path])
      await env.writeFile(path, Buffer.from('{ "value": 1 }\n\n'))
      const changed = await env.snapshot([path])
      expect(changed.entries).toEqual([{ path, state: { kind: 'file', bytes: Buffer.from('{ "value": 1 }\n\n') } }])
      expect(await env.run({ args: ['rm', path] })).toEqual(empty)
      expect((await env.snapshot([path])).entries).toEqual([{ path, state: { kind: 'missing' } }])
      expect(before.entries).toEqual([{ path, state: { kind: 'file', bytes: Buffer.from('{"value":1}\n') } }])
    },
    signal,
  )
})

test.concurrent('filesystem snapshots reject special files and non-directory path errors', async ({
  expect,
  signal,
}) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      expect(await env.run({ args: ['mkfifo', '/workspace/fifo'] })).toEqual(empty)
      await expect(env.snapshot(['/workspace/fifo'])).rejects.toThrow(/unsupported/i)
      await env.writeFile('/workspace/file', Buffer.from('file'))
      await expect(env.snapshot(['/workspace/file/child'])).rejects.toThrow(/not a directory/i)
    },
    signal,
  )
})

test.concurrent('filesystem snapshots preserve arbitrary literal link target bytes', async ({ expect, signal }) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      expect(await env.run({ args: ['sh', '-c', 'ln -s "$(printf "\\377")" /workspace/raw-link'] })).toEqual(empty)
      expect((await env.snapshot(['/workspace/raw-link'])).entries).toEqual([
        { path: '/workspace/raw-link', state: { kind: 'symlink', target: Buffer.from([255]) } },
      ])
    },
    signal,
  )
})

test.concurrent('terminal keeps raw stdout separate and observes restored termios after input', async ({
  expect,
  signal,
}) => {
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
    signal,
  )
})

test.concurrent('terminal finite pipe input reports unread bytes and application failure', async ({
  expect,
  signal,
}) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      const input = Buffer.from([0, 255, 13, 10])
      await withTerminal(env, { args: ['sh', '-c', 'cat; printf eof >&2'] }, { input }, async (session) => {
        const done = await session.wait()
        expect(done.stdout).toEqual(input)
        expect(done.terminal).toEqual(Buffer.from('eof'))
        expect(done.unreadInput).toEqual(Buffer.alloc(0))
        expect(done.status).toEqual({ kind: 'exited', code: 0 })
      })
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
    signal,
  )
})

test.concurrent('non-root images install executable drivers without changing ordinary file modes', async ({
  expect,
  signal,
}) => {
  const nonrootImage = 'e2e-example-nonroot:local'
  const original = await docker('image', 'inspect', '--format', '{{.Id}}', nonrootImage)
  await withEnvironment(
    nonrootImage,
    async (env) => {
      await checkIdentity(env, original)
      expect((await env.run({ args: ['id', '-u'] })).stdout.trim()).not.toBe('0')
      await installFixtureDriver(env)
      await env.writeFile('/tmp/nonroot-file', Buffer.from([0, 255]))
      expect(await env.run({ args: ['stat', '-c', '%a', '/tmp/nonroot-file'] })).toEqual({ ...empty, stdout: '644\n' })
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
    signal,
  )
  expect(await docker('image', 'inspect', '--format', '{{.Id}}', nonrootImage)).toBe(original)
})

test.concurrent('terminal resize reaches the foreground process and retains trailing matches', async ({
  expect,
  signal,
}) => {
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
    signal,
  )
})

test.concurrent('terminal expectation timeout retains diagnostics and literal Ctrl-D supplies EOF', async ({
  expect,
  signal,
}) => {
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
        expect(await env.pathKind('/workspace')).toBe('directory')
        await session.send(Buffer.from([4]))
        const result = await session.wait()
        expect(result.status).toEqual({ kind: 'exited', code: 0 })
        expect(result.stdout).toEqual(Buffer.alloc(0))
        expect(result.termiosRestored).toBe(true)
      })
    },
    signal,
  )
})

test.concurrent('terminal signals and callback failure clean up a surviving process and invalidate handles', async ({
  expect,
  signal,
}) => {
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
            `saved=$(stty -g < /dev/tty); stty raw -echo < /dev/tty; trap 'stty "$saved" < /dev/tty; printf "\\033[?25h\\033[?1049l" >&2; exit 130' INT; printf '\\033[?1049h\\033[?25lready' >&2; while :; do sleep 0.02; done`,
          ],
        },
        {},
        async (session) => {
          await session.expect('terminal', Buffer.from('ready'))
          await session.signal('SIGINT')
          const done = await session.wait()
          expect(done.status).toEqual({ kind: 'exited', code: 130 })
          expect(done.terminal).toEqual(Buffer.from('\x1b[?1049h\x1b[?25lready\x1b[?25h\x1b[?1049l'))
          expect(done.termiosRestored).toBe(true)
          expect(done.currentTermios).toEqual(done.initialTermios)
        },
      )
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
      expect(await env.run({ args: ['sh', '-c', 'find /tmp -maxdepth 1 -name "caseyard-pty-*" -print'] })).toEqual(
        empty,
      )
    },
    signal,
  )
})

test.concurrent('terminal raw keys preserve argv cwd HOME TERM and repeated split prompts', async ({
  expect,
  signal,
}) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      const keys = Buffer.from([27, 91, 65, 27, 91, 66, 32, 13, 27, 4, 3])
      await withTerminal(
        env,
        {
          args: [
            'sh',
            '-c',
            `saved=$(stty -g < /dev/tty); stty raw -echo < /dev/tty; printf pro >&2; printf fragment; dd bs=1 count=1 2>/dev/null >/dev/null; printf 'mpt>' >&2; dd bs=1 count=11 2>/dev/null; printf 'prompt>' >&2; dd bs=1 count=1 2>/dev/null; stty "$saved" < /dev/tty; printf '\\000%s\\000%s\\000%s\\000%s\\000%s\\000%s' "$1" "$2" "$3" "$PWD" "$HOME" "$TERM"`,
            'fixture',
            'a b',
            '',
            '$(literal)',
          ],
          workingDir: '/workspace',
          env: ['HOME=/workspace/caller-home', 'TERM=xterm-256color'],
        },
        {},
        async (terminal) => {
          await terminal.expect('stdout', Buffer.from('fragment'))
          await expect(terminal.expect('terminal', Buffer.from('prompt>'), 0.1)).rejects.toMatchObject({
            name: 'TerminalError',
            observation: { terminal: Buffer.from('pro') },
          })
          await terminal.send(Buffer.from('!'))
          await terminal.expect('terminal', Buffer.from('prompt>'))
          await terminal.send(keys)
          await terminal.expect('terminal', Buffer.from('prompt>'))
          await expect(terminal.expect('terminal', Buffer.from('prompt>'), 0.1)).rejects.toMatchObject({
            name: 'TerminalError',
          })
          await terminal.send(Buffer.from('x'))
          const done = await terminal.wait()
          expect(done.stdout).toEqual(
            Buffer.concat([
              Buffer.from('fragment'),
              keys,
              Buffer.from('x\0a b\0\0$(literal)\0/workspace\0/workspace/caller-home\0xterm-256color'),
            ]),
          )
          expect(done.terminal).toEqual(Buffer.from('prompt>prompt>'))
          expect(done.status).toEqual({ kind: 'exited', code: 0 })
          expect(done.termiosRestored).toBe(true)
        },
      )
    },
    signal,
  )
})

test.concurrent('terminal unsupported TERM is observable without changing explicit filesystem state', async ({
  expect,
  signal,
}) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      await env.writeFile('/workspace/config', Buffer.from([0, 255]))
      const before = await env.snapshot(['/workspace/config', '/workspace/absent'])
      await withTerminal(
        env,
        {
          args: [
            'sh',
            '-c',
            'test "$TERM" = unsupported-caseyard && { printf "unsupported:%s" "$TERM" >&2; exit 7; }; exit 9',
          ],
          env: ['TERM=unsupported-caseyard'],
        },
        {},
        async (terminal) => {
          const done = await terminal.wait()
          expect(done.stdout).toEqual(Buffer.alloc(0))
          expect(done.terminal).toEqual(Buffer.from('unsupported:unsupported-caseyard'))
          expect(done.status).toEqual({ kind: 'exited', code: 7 })
          expect(done.termiosRestored).toBe(true)
        },
      )
      expect(await env.snapshot(before.entries.map(({ path }) => path))).toEqual(before)
    },
    signal,
  )
})

test.concurrent('terminal tiny Enter is a no-op before long-list resize recovery and Escape cancellation', async ({
  expect,
  signal,
}) => {
  await withEnvironment(
    image,
    async (env) => {
      await checkIdentity(env)
      await installFixtureDriver(env)
      await env.writeFile('/workspace/config', Buffer.from([0, 255]))
      expect(await env.run({ args: ['ln', '-s', 'config', '/workspace/config-link'] })).toEqual(empty)
      const before = await env.snapshot(['/workspace/config', '/workspace/config-link', '/workspace/absent'])
      const list = Buffer.from(
        Array.from({ length: 4096 }, (_, index) => `row${String(index).padStart(4, '0')}\n`).join(''),
      )
      await withTerminal(
        env,
        {
          args: [
            'sh',
            '-c',
            `saved=$(stty -g < /dev/tty); stty raw -echo < /dev/tty; trap 'stty "$saved" < /dev/tty; printf "\\033[?25h\\033[?1049l" >&2' EXIT; printf '\\033[?1049h\\033[?25ltiny>' >&2; dd bs=1 count=1 2>/dev/null >/dev/null; resized=no; trap 'resized=yes' WINCH; printf ignored >&2; while [ "$resized" = no ]; do sleep 0.02; done; stty size < /dev/tty >&2; i=0; while [ "$i" -lt 4096 ]; do printf 'row%04d\\n' "$i" >&2; i=$((i+1)); done; printf 'list-end>' >&2; dd bs=1 count=3 2>/dev/null >/dev/null; printf 'page4095>' >&2; dd bs=1 count=3 2>/dev/null >/dev/null; printf 'page4094>' >&2; dd bs=1 count=1 2>/dev/null >/dev/null`,
          ],
        },
        { rows: 1, columns: 1 },
        async (terminal) => {
          await terminal.expect('terminal', Buffer.from('tiny>'))
          await terminal.send(Buffer.from([13]))
          await terminal.expect('terminal', Buffer.from('ignored'))
          expect(await env.snapshot(before.entries.map(({ path }) => path))).toEqual(before)
          expect((await terminal.observe()).status).toEqual({ kind: 'running' })
          await terminal.resize(24, 80)
          await terminal.expect('terminal', Buffer.from('24 80\n'))
          await terminal.expect('terminal', Buffer.from('list-end>'))
          await terminal.send(Buffer.from('\x1b[B'))
          await terminal.expect('terminal', Buffer.from('page4095>'))
          await terminal.send(Buffer.from('\x1b[A'))
          await terminal.expect('terminal', Buffer.from('page4094>'))
          await terminal.send(Buffer.from([27]))
          const done = await terminal.wait()
          expect(done.stdout).toEqual(Buffer.alloc(0))
          expect(done.terminal).toEqual(
            Buffer.concat([
              Buffer.from('\x1b[?1049h\x1b[?25ltiny>ignored24 80\n'),
              list,
              Buffer.from('list-end>page4095>page4094>\x1b[?25h\x1b[?1049l'),
            ]),
          )
          expect(done.status).toEqual({ kind: 'exited', code: 0 })
          expect(done.termiosRestored).toBe(true)
        },
      )
      expect(await env.snapshot(before.entries.map(({ path }) => path))).toEqual(before)
    },
    signal,
  )
})
