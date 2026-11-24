import { withEnvironment } from '@totto2727/caseyard'
import { describe, test } from 'vitest'

describe('multiple scenarios', () => {
  test.concurrent('prints greeting', async ({ expect, signal }) => {
    const result = await withEnvironment(
      'example-cli:local',
      async (environment) => await environment.run({ args: ['echo', 'hello from e2e'] }),
      signal,
    )
    expect(result).toStrictEqual({ exitCode: 0, stderr: '', stdout: 'hello from e2e\n' })
  })

  test.concurrent('returns expected failure', async ({ expect, signal }) => {
    const result = await withEnvironment(
      'example-cli:local',
      async (environment) => await environment.run({ args: ['false'] }),
      signal,
    )
    expect(result).toStrictEqual({ exitCode: 1, stderr: '', stdout: '' })
  })
})
