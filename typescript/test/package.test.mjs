import { createRequire } from 'node:module'

import * as cli from '@totto2727/e2e'
import { expect, test } from 'vitest'

test('built package exposes only the public API through its declared entrypoints', () => {
  expect(Object.keys(cli).sort()).toEqual(['DeadlineError', 'DockerError', 'ExecutionError', 'withEnvironment'])
  expect(cli.withEnvironment).toBeTypeOf('function')
  const error = new cli.ExecutionError({ stdout: 'captured', stderr: '' }, new cli.DeadlineError())
  expect(error).toMatchObject({ partialResult: { stdout: 'captured', stderr: '' }, cause: { name: 'DeadlineError' } })
})

test('does not expose a retired framework adapter or transport internals', () => {
  const require = createRequire(import.meta.url)
  for (const path of ['@totto2727/e2e/vitest', '@totto2727/e2e/runner']) {
    expect(() => require.resolve(path)).toThrow(expect.objectContaining({ code: 'ERR_PACKAGE_PATH_NOT_EXPORTED' }))
  }
})
