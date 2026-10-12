import { withEnvironment, type Environment } from '@totto2727/e2e-cli'
import { test } from 'vitest'

test.concurrent('prints greeting', async (context) => {
  await withEnvironment('example-cli:local', printsGreetingScenario, context.signal)
})

test.concurrent('returns expected failure', async (context) => {
  await withEnvironment('example-cli:local', returnsExpectedFailureScenario, context.signal)
})

async function printsGreetingScenario(environment: Environment): Promise<void> {
  await environment.checkCommand(
    { args: ['echo', 'hello from e2e'] },
    { exitCode: 0, stdout: 'hello from e2e\n', stderr: '' },
  )
}

async function returnsExpectedFailureScenario(environment: Environment): Promise<void> {
  await environment.checkCommand({ args: ['false'] }, { exitCode: 1, stdout: '', stderr: '' })
}
