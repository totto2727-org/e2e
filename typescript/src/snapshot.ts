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

export interface SnapshotDifference {
  readonly path: string
  readonly reason: 'existence' | 'kind' | 'content' | 'target'
}

function copyState(state: FileState): FileState {
  if (state.kind === 'symlink') return Object.freeze({ kind: 'symlink', target: Buffer.from(state.target) })
  return Object.freeze(state.kind === 'file' ? { kind: 'file', bytes: Buffer.from(state.bytes) } : { ...state })
}

/** Read-only captured state. Every returned file byte buffer is a detached copy. */
export class FilesystemSnapshot {
  readonly #entries: readonly SnapshotEntry[]

  constructor(entries: readonly SnapshotEntry[]) {
    const paths = new Set<string>()
    this.#entries = Object.freeze(
      entries
        .map(({ path, state }) => {
          if (!path || path.includes('\0')) throw new TypeError('Snapshot paths must be nonempty NUL-free strings')
          if (paths.has(path)) throw new TypeError(`Duplicate snapshot path: ${path}`)
          paths.add(path)
          return Object.freeze({ path, state: copyState(state) })
        })
        .sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0)),
    )
    Object.freeze(this)
  }

  get entries(): readonly SnapshotEntry[] {
    return Object.freeze(this.#entries.map(({ path, state }) => Object.freeze({ path, state: copyState(state) })))
  }
}

/** Compare the union of explicit paths. No filesystem access or baseline updates occur. */
export function compareSnapshots(
  expected: FilesystemSnapshot,
  actual: FilesystemSnapshot,
): readonly SnapshotDifference[] {
  const before = new Map(expected.entries.map(({ path, state }) => [path, state]))
  const after = new Map(actual.entries.map(({ path, state }) => [path, state]))
  const differences: SnapshotDifference[] = []
  for (const path of [...new Set([...before.keys(), ...after.keys()])].sort()) {
    const left = before.get(path)
    const right = after.get(path)
    let reason: SnapshotDifference['reason'] | undefined
    if (!left || !right || (left.kind === 'missing') !== (right.kind === 'missing')) reason = 'existence'
    else if (left.kind !== right.kind) reason = 'kind'
    else if (left.kind === 'file' && right.kind === 'file' && !left.bytes.equals(right.bytes)) reason = 'content'
    else if (left.kind === 'symlink' && right.kind === 'symlink' && !left.target.equals(right.target)) reason = 'target'
    if (reason) differences.push(Object.freeze({ path, reason }))
  }
  return Object.freeze(differences)
}

export async function captureSnapshot(environment: Environment, paths: readonly string[]): Promise<FilesystemSnapshot> {
  const ordered = [...new Set(paths)].sort()
  for (const path of ordered)
    if (typeof path !== 'string' || !path || path.includes('\0'))
      throw new TypeError('Snapshot paths must be nonempty NUL-free strings')
  if (!ordered.length) return new FilesystemSnapshot([])
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
        entries.push({
          path,
          state: { kind: 'symlink', target },
        })
        break
      default:
        throw new Error('Unsupported snapshot state')
    }
  }
  return new FilesystemSnapshot(entries)
}
