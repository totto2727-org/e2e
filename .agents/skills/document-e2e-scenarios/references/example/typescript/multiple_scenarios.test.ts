import type { Environment } from '@totto2727/e2e-cli'
import { registerCases } from '@totto2727/e2e-cli/vitest'

registerCases('example-cli:local', [
  { name: 'prints greeting', run: printsGreetingScenario },
  { name: 'returns expected failure', run: returnsExpectedFailureScenario },
])

async function printsGreetingScenario(environment: Environment): Promise<void> {
  await environment.checkCommand(
    { args: ['echo', 'hello from e2e'] },
    { exitCode: 0, stdout: 'hello from e2e\n', stderr: '' },
  )
}

async function returnsExpectedFailureScenario(environment: Environment): Promise<void> {
  await environment.checkCommand({ args: ['false'] }, { exitCode: 1, stdout: '', stderr: '' })
}
