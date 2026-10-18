import { invokeDriver, driverRecord, driverBytes } from './driver.js'
import type { Environment } from './environment.js'

export type FileState =
  | { readonly kind: 'missing' }
  | { readonly kind: 'file'; readonly bytes: Buffer }
  | { readonly kind: 'directory' }
  | { readonly kind: 'symlink'; readonly target: Buffer }

export interface SnapshotEntry {
  readonly path: string
  readonly state: FileState
}

/** Plain captured state. File and link-target buffers are detached on every read. */
export interface FilesystemSnapshot {
  readonly entries: readonly SnapshotEntry[]
}

function copyState(state: FileState): FileState {
  if (state.kind === 'symlink') return Object.freeze({ kind: 'symlink', target: Buffer.from(state.target) })
  return Object.freeze(state.kind === 'file' ? { kind: 'file', bytes: Buffer.from(state.bytes) } : { ...state })
}

function snapshot(entries: readonly SnapshotEntry[]): FilesystemSnapshot {
  return Object.freeze({
    get entries(): readonly SnapshotEntry[] {
      return Object.freeze(entries.map(({ path, state }) => Object.freeze({ path, state: copyState(state) })))
    },
  })
}

export async function captureSnapshot(environment: Environment, paths: readonly string[]): Promise<FilesystemSnapshot> {
  const ordered = [...new Set(paths)].sort()
  for (const path of ordered)
    if (typeof path !== 'string' || !path || path.includes('\0'))
      throw new TypeError('Snapshot paths must be nonempty NUL-free strings')
  if (!ordered.length) return snapshot([])
  const states = await invokeDriver(environment, 'snapshot', { paths: ordered })
  if (!Array.isArray(states) || states.length !== ordered.length)
    throw new Error('Invalid snapshot classification response')
  const entries: SnapshotEntry[] = []
  for (const [index, path] of ordered.entries()) {
    const state = driverRecord(states[index])
    if (state['path'] !== path) throw new Error('Invalid snapshot path')
    const data = driverBytes(state['data'])
    const target = driverBytes(state['target'])
    switch (state['kind']) {
      case 'missing':
      case 'directory':
        if (data.length || target.length) throw new Error('Invalid snapshot state payload')
        entries.push({ path, state: { kind: state['kind'] } })
        break
      case 'file':
        if (target.length) throw new Error('Invalid regular file target')
        entries.push({ path, state: { kind: 'file', bytes: data } })
        break
      case 'symlink':
        if (data.length) throw new Error('Invalid symbolic link content')
        entries.push({ path, state: { kind: 'symlink', target } })
        break
      default:
        throw new Error('Unsupported snapshot state')
    }
  }
  return snapshot(entries)
}
