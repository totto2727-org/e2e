import { withEnvironment } from '@totto2727/e2e'
import { test } from 'vitest'

test.concurrent('prints greeting', async (context) => {
  await withEnvironment(
    'example-cli:local',
    async (environment) => {
      await environment.checkCommand(
        { args: ['echo', 'hello from e2e'] },
        { exitCode: 0, stdout: 'hello from e2e\n', stderr: '' },
      )
    },
    context.signal,
  )
})

test.concurrent('returns expected failure', async (context) => {
  await withEnvironment(
    'example-cli:local',
    async (environment) => {
      await environment.checkCommand({ args: ['false'] }, { exitCode: 1, stdout: '', stderr: '' })
    },
    context.signal,
  )
})
