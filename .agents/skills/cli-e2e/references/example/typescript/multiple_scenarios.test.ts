import { withEnvironment } from '@totto2727/caseyard'
import { test } from 'vitest'

test.concurrent('prints greeting', async (context) => {
  await withEnvironment(
    'example-cli:local',
    async (environment) => {
      context
        .expect(await environment.run({ args: ['echo', 'hello from e2e'] }))
        .toEqual({ exitCode: 0, stdout: 'hello from e2e\n', stderr: '' })
    },
    context.signal,
  )
})

test.concurrent('returns expected failure', async (context) => {
  await withEnvironment(
    'example-cli:local',
    async (environment) => {
      context.expect(await environment.run({ args: ['false'] })).toEqual({ exitCode: 1, stdout: '', stderr: '' })
    },
    context.signal,
  )
})
