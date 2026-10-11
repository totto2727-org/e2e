import { afterAll, beforeAll, expect } from 'vitest'

import { Engine, json, stringField } from '../src/engine.js'
import { DeadlineError, type Case } from '../src/index.js'
import { registerCases } from '../src/vitest.js'

const image = 'e2e-example:local'
const engine = new Engine()
const identities: string[] = []
let imageID = ''
let baseline: string[] = []

async function containers(): Promise<string[]> {
  const filters = encodeURIComponent(JSON.stringify({ label: ['org.totto2727.e2e.owner'] }))
  const value = await json(engine, 'GET', `/containers/json?all=true&filters=${filters}`, AbortSignal.timeout(30_000))
  if (!Array.isArray(value)) throw new Error('Expected Docker container list')
  return value.map((container: unknown) => stringField(container, 'Id')).sort()
}
beforeAll(async () => {
  baseline = await containers()
  imageID = stringField(
    await json(engine, 'GET', `/images/${encodeURIComponent(image)}/json`, AbortSignal.timeout(30_000)),
    'Id',
  )
}, 0)
afterAll(async () => {
  expect(identities).toHaveLength(6)
  expect(new Set(identities).size).toBe(6)
  expect(await containers()).toEqual(baseline)
  expect(
    stringField(
      await json(engine, 'GET', `/images/${encodeURIComponent(image)}/json`, AbortSignal.timeout(30_000)),
      'Id',
    ),
  ).toBe(imageID)
}, 0)

const empty = { exitCode: 0, stdout: '', stderr: '' }
const cases: Case[] = [
  {
    name: 'greeting',
    async run(env) {
      await env.checkCommand(
        { args: ['echo', 'hello from e2e'] },
        { exitCode: 0, stdout: 'hello from e2e\n', stderr: '' },
      )
    },
  },
  {
    name: 'file workflow',
    async run(env) {
      await env.checkCommand({ args: ['sh', '-c', 'echo first > /workspace/result.txt'] }, empty)
      await env.checkCommand({ args: ['sh', '-c', 'echo second >> /workspace/result.txt'] }, empty)
      await env.checkCommand({ args: ['cat', '/workspace/result.txt'] }, { ...empty, stdout: 'first\nsecond\n' })
      await env.checkFile('/workspace/result.txt', Buffer.from('first\nsecond\n'))
      const bytes = Buffer.from([0, 255, 10, 13, 1])
      await env.writeFile('/workspace/binary', bytes)
      expect(await env.readFile('/workspace/binary')).toEqual(bytes)
      await env.checkCommand({ args: ['stat', '-c', '%a', '/workspace/binary'] }, { ...empty, stdout: '644\n' })
    },
  },
  {
    name: 'expected failure',
    async run(env) {
      await env.checkCommand(
        { args: ['sh', '-c', 'echo expected-failure; exit 7'] },
        { exitCode: 7, stdout: 'expected-failure\n', stderr: '' },
      )
    },
  },
  {
    name: 'separate streams',
    async run(env) {
      await env.checkCommand(
        {
          args: ['sh', '-c', 'printf "%s:%s\\n" "$HOME" "$PWD"; printf "diagnostic\\n" >&2; exit 7'],
          workingDir: '/workspace',
          env: ['HOME=/workspace/synthetic-home'],
        },
        {
          exitCode: 7,
          stdout: '/workspace/synthetic-home:/workspace\n',
          stderr: 'diagnostic\n',
        },
      )
    },
  },
  {
    name: 'physical paths',
    async run(env) {
      await env.writeFile('/workspace/result.txt', Buffer.from('fixture\n'))
      for (const args of [
        ['mkdir', '/workspace/directory'],
        ['ln', '-s', 'result.txt', '/workspace/link'],
        ['ln', '-s', 'missing.txt', '/workspace/dangling'],
        ['ln', '-s', 'directory', '/workspace/directory-link'],
      ]) {
        await env.checkCommand({ args }, empty)
      }
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
  },
  {
    name: 'deadline',
    async run(env) {
      const started = Date.now()
      await expect(env.run({ args: ['sleep', 'infinity'] })).rejects.toMatchObject({
        name: 'ExecutionError',
        cause: new DeadlineError(),
        partialResult: { stdout: '', stderr: '' },
      })
      expect(Date.now() - started).toBeLessThan(70_000)
    },
  },
]
registerCases(
  image,
  cases.map((testCase) => ({
    name: testCase.name,
    async run(env) {
      const hostname = await env.run({ args: ['hostname'] })
      expect(hostname.exitCode).toBe(0)
      expect(hostname.stderr).toBe('')
      const id = hostname.stdout.trim()
      expect(id).toMatch(/^[a-f0-9]{12}$/)
      identities.push(id)
      const inspected = await json(engine, 'GET', `/containers/${id}/json`, env.signal)
      expect(inspected).toMatchObject({ Image: imageID })
      await testCase.run(env)
    },
  })),
)
