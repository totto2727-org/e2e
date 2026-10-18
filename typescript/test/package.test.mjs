import { createRequire } from 'node:module'

import * as cli from '@totto2727/caseyard'
import { expect, test } from 'vitest'

test('built package exposes only the public API through its declared entrypoints', () => {
  expect(Object.keys(cli).sort()).toEqual([
    'DeadlineError',
    'DockerError',
    'ExecutionError',
    'FilesystemSnapshot',
    'TerminalError',
    'compareSnapshots',
    'installDriver',
    'withEnvironment',
    'withTerminal',
  ])
  expect(cli.withEnvironment).toBeTypeOf('function')
  expect(cli.withTerminal).toBeTypeOf('function')
  expect(cli.installDriver).toBeTypeOf('function')
  const captured = { stdout: Buffer.from([255]), terminal: Buffer.from([27]) }
  expect(new cli.TerminalError('missing prompt', captured)).toMatchObject({
    name: 'TerminalError',
    observation: captured,
  })
  const error = new cli.ExecutionError({ stdout: 'captured', stderr: '' }, new cli.DeadlineError())
  expect(error).toMatchObject({ partialResult: { stdout: 'captured', stderr: '' }, cause: { name: 'DeadlineError' } })
})

test('built snapshot exports compare exact bytes and protect captured buffers', () => {
  const bytes = Buffer.from([0, 255])
  const snapshot = new cli.FilesystemSnapshot([{ path: '/file', state: { kind: 'file', bytes } }])
  bytes.fill(0)
  expect(
    cli.compareSnapshots(
      snapshot,
      new cli.FilesystemSnapshot([{ path: '/file', state: { kind: 'file', bytes: Buffer.from([0, 254]) } }]),
    ),
  ).toEqual([{ path: '/file', reason: 'content' }])
  expect(cli.compareSnapshots(snapshot, snapshot)).toEqual([])
  const target = Buffer.from([255])
  const link = new cli.FilesystemSnapshot([{ path: '/link', state: { kind: 'symlink', target } }])
  target.fill(0)
  expect(link.entries).toEqual([{ path: '/link', state: { kind: 'symlink', target: Buffer.from([255]) } }])
})

test('does not expose a retired framework adapter or transport internals', () => {
  const require = createRequire(import.meta.url)
  for (const path of ['@totto2727/caseyard/vitest', '@totto2727/caseyard/runner']) {
    expect(() => require.resolve(path)).toThrow(expect.objectContaining({ code: 'ERR_PACKAGE_PATH_NOT_EXPORTED' }))
  }
})
