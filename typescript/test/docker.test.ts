import { execFile } from 'node:child_process'
import { promisify } from 'node:util'

import { afterAll, beforeAll, expect, test } from 'vitest'

import { DeadlineError, withEnvironment, type Environment } from '../src/index.js'

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

async function checkIdentity(env: Environment): Promise<void> {
  const hostname = await env.run({ args: ['hostname'] })
  expect(hostname.exitCode).toBe(0)
  expect(hostname.stderr).toBe('')
  const id = hostname.stdout.trim()
  expect(id).toMatch(/^[a-f0-9]{12}$/)
  identities.push(id)
  expect(await docker('inspect', '--format', '{{.Image}}', id)).toBe(imageID)
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
