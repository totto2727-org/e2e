import { expect, test } from 'vitest'

import * as cli from '@totto2727/e2e-cli'
import * as integration from '@totto2727/e2e-cli/vitest'

test('built package exposes only the public API through its declared entrypoints', () => {
  expect(Object.keys(cli).sort()).toEqual(['DeadlineError', 'DockerError', 'ExecutionError', 'runCases'])
  expect(Object.keys(integration)).toEqual(['registerCases'])
  expect(cli.runCases).toBeTypeOf('function')
  expect(integration.registerCases).toBeTypeOf('function')
  const error = new cli.ExecutionError({ stdout: 'captured', stderr: '' }, new cli.DeadlineError())
  expect(error).toMatchObject({ partialResult: { stdout: 'captured', stderr: '' }, cause: { name: 'DeadlineError' } })
})
