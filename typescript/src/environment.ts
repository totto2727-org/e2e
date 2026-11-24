import nodePath from 'node:path'
import { pipeline } from 'node:stream/promises'

import tar from 'tar-stream'
import type { StartedTestContainer } from 'testcontainers'

import { untilCancelled } from './cancellation.ts'
import { registerDriverWriter, registerDriverCleanup } from './driver.ts'
import { DockerError, dockerFailure } from './errors.ts'
import { ExecutionError } from './execution-error.ts'
import { captureSnapshot } from './snapshot.ts'
import type { FilesystemSnapshot } from './snapshot.ts'

export type PathKind = 'missing' | 'file' | 'directory' | 'symlink' | 'other'

export interface Command {
  args: readonly string[]
  workingDir?: string
  env?: readonly string[]
}
export interface Result {
  exitCode: number
  stdout: string
  stderr: string
}
export interface PartialResult {
  stdout: string
  stderr: string
  exitCode?: never
}

const text = (value: unknown, description: string): void => {
  if (typeof value !== 'string' || value.includes('\0')) {
    throw new TypeError(`${description} must be a NUL-free string`)
  }
}
const pathValue = (path: string): void => {
  text(path, 'Path')
  if (!path) {
    throw new TypeError('Path must not be empty')
  }
}
export const validateCommand = (command: Command): void => {
  const args: readonly unknown[] = command.args
  if (!Array.isArray(args) || typeof args[0] !== 'string' || args[0].length === 0) {
    throw new TypeError('Command requires a nonempty executable')
  }
  for (const arg of args) {
    text(arg, 'Argument')
  }
  if (command.workingDir !== undefined) {
    text(command.workingDir, 'Working directory')
  }
  for (const entry of command.env ?? []) {
    text(entry, 'Environment entry')
  }
}

export interface Environment {
  readonly signal: AbortSignal
  run: (command: Command) => Promise<Result>
  writeFile: (path: string, bytes: Uint8Array) => Promise<void>
  readFile: (path: string) => Promise<Buffer>
  pathKind: (path: string) => Promise<PathKind>
  readSymlink: (path: string) => Promise<string>
  snapshot: (paths: readonly string[]) => Promise<FilesystemSnapshot>
}

const closeLateArchive = async (copying: Promise<NodeJS.ReadableStream>): Promise<void> => {
  try {
    const stream = await copying
    if ('destroy' in stream && typeof stream.destroy === 'function') {
      // oxlint-disable-next-line typescript/no-unsafe-call -- The stream's optional destroy method has no public type.
      stream.destroy()
    }
  } catch {
    // The original error is already returned to the caller.
  }
}

export class DockerEnvironment implements Environment {
  private readonly container: StartedTestContainer
  readonly signal: AbortSignal

  constructor(container: StartedTestContainer, signal: AbortSignal) {
    this.container = container
    this.signal = signal
    registerDriverWriter(this, async (path, bytes) => {
      await this.write(path, bytes, 0o755)
    })
    registerDriverCleanup(this, async (command, cleanupSignal) => await this.execute(command, cleanupSignal))
  }

  async snapshot(paths: readonly string[]): Promise<FilesystemSnapshot> {
    this.signal.throwIfAborted()
    return await captureSnapshot(this, paths)
  }

  async run(command: Command): Promise<Result> {
    return await this.execute(command, this.signal)
  }

  private async execute(command: Command, signal: AbortSignal): Promise<Result> {
    validateCommand(command)
    const env = Object.fromEntries(
      (command.env ?? []).map((entry) => {
        const separator = entry.indexOf('=')
        if (separator < 1) {
          throw new TypeError('Environment entries must use NAME=value')
        }
        return [entry.slice(0, separator), entry.slice(separator + 1)]
      }),
    )
    try {
      signal.throwIfAborted()
      const result = await untilCancelled(
        this.container.exec([...command.args], {
          ...(command.workingDir === undefined ? {} : { workingDir: command.workingDir }),
          env,
        }),
        signal,
      )
      if (!Number.isInteger(result.exitCode) || result.exitCode < 0) {
        throw new DockerError('Testcontainers did not report an application exit code')
      }
      return { exitCode: result.exitCode, stderr: result.stderr, stdout: result.stdout }
    } catch (error) {
      // Testcontainers exposes output only after exec succeeds, never on rejection.
      const cause: unknown = signal.aborted ? signal.reason : dockerFailure(error)
      throw new ExecutionError(
        { stderr: '', stdout: '' },
        error instanceof DockerError && !signal.aborted ? error : cause,
      )
    }
  }

  async writeFile(path: string, bytes: Uint8Array): Promise<void> {
    await this.write(path, bytes, 0o644)
  }

  private async write(filePath: string, bytes: Uint8Array, mode: number): Promise<void> {
    pathValue(filePath)
    this.signal.throwIfAborted()
    if (!(await this.probe('-d', nodePath.posix.dirname(filePath)))) {
      throw new Error(`Parent directory does not exist: ${filePath}`)
    }
    try {
      await untilCancelled(
        this.container.copyContentToContainer([{ content: Buffer.from(bytes), mode, target: filePath }]),
        this.signal,
      )
    } catch (error) {
      throw this.signal.aborted ? this.signal.reason : dockerFailure(error)
    }
  }

  async readFile(path: string): Promise<Buffer> {
    pathValue(path)
    this.signal.throwIfAborted()
    const copying = this.container.copyArchiveFromContainer(path)
    const readArchive = async (): Promise<NodeJS.ReadableStream> => {
      try {
        return await untilCancelled(copying, this.signal)
      } catch (error) {
        // A response that arrives after cancellation is still ours to close.
        void closeLateArchive(copying)
        this.signal.throwIfAborted()
        throw dockerFailure(error)
      }
    }
    const archive = await readArchive()
    const extract = tar.extract()
    const files: Buffer[] = []
    const observation = { invalid: false }
    extract.on('entry', (header, stream, next) => {
      if (header.type !== 'file') {
        observation.invalid = true
      }
      const chunks: Buffer[] = []
      stream.on('data', (chunk: unknown) => {
        if (!(chunk instanceof Uint8Array)) {
          extract.destroy(new DockerError('Expected archive byte stream'))
          return
        }
        chunks.push(Buffer.from(chunk))
      })
      stream.on('error', (error: Error) => {
        extract.destroy(error)
      })
      stream.on('end', () => {
        files.push(Buffer.concat(chunks))
        next()
      })
    })
    try {
      await pipeline(archive, extract, { signal: this.signal })
      const [file] = files
      if (observation.invalid || files.length !== 1 || file === undefined) {
        throw new DockerError('Expected one regular file in Docker archive')
      }
      return file
    } catch (error) {
      this.signal.throwIfAborted()
      throw error
    }
  }

  private async probe(flag: string, path: string): Promise<boolean> {
    pathValue(path)
    const result = await this.run({ args: ['test', flag, path] })
    if (result.exitCode !== 0 && result.exitCode !== 1) {
      throw new DockerError(`Path probe failed: ${JSON.stringify(result)}`)
    }
    return result.exitCode === 0
  }

  async pathKind(path: string): Promise<PathKind> {
    if (await this.probe('-L', path)) {
      return 'symlink'
    }
    if (await this.probe('-f', path)) {
      return 'file'
    }
    if (await this.probe('-d', path)) {
      return 'directory'
    }
    if (!(await this.probe('-e', path))) {
      return 'missing'
    }
    return 'other'
  }

  async readSymlink(path: string): Promise<string> {
    pathValue(path)
    const result = await this.run({ args: ['readlink', '--', path] })
    if (result.exitCode !== 0 || result.stderr !== '' || !result.stdout.endsWith('\n')) {
      throw new DockerError(`Symbolic link read failed: ${JSON.stringify(result)}`)
    }
    return result.stdout.slice(0, -1)
  }
}
