import { posix } from 'node:path'
import { pipeline } from 'node:stream/promises'

import tar from 'tar-stream'
import type { StartedTestContainer } from 'testcontainers'

import { untilCancelled } from './cancellation.js'
import { registerDriverWriter, registerDriverCleanup } from './driver.js'
import { DockerError, dockerFailure } from './errors.js'
import { captureSnapshot, compareSnapshots, type FilesystemSnapshot } from './snapshot.js'

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

export class ExecutionError extends Error {
  readonly partialResult: PartialResult

  constructor(partialResult: PartialResult, cause: unknown) {
    super('Command execution failed', { cause })
    this.partialResult = partialResult
    this.name = 'ExecutionError'
  }
}

function text(value: string, description: string): void {
  if (typeof value !== 'string' || value.includes('\0')) throw new TypeError(`${description} must be a NUL-free string`)
}
function pathValue(path: string): void {
  text(path, 'Path')
  if (!path) throw new TypeError('Path must not be empty')
}
export function validateCommand(command: Command): void {
  if (!Array.isArray(command.args) || !command.args.length || !command.args[0])
    throw new TypeError('Command requires a nonempty executable')
  for (const arg of command.args) text(arg, 'Argument')
  if (command.workingDir !== undefined) text(command.workingDir, 'Working directory')
  for (const entry of command.env ?? []) text(entry, 'Environment entry')
}

export interface Environment {
  readonly signal: AbortSignal
  run(command: Command): Promise<Result>
  checkCommand(command: Command, expected: Result): Promise<void>
  writeFile(path: string, bytes: Uint8Array): Promise<void>
  readFile(path: string): Promise<Buffer>
  checkFile(path: string, expected: Uint8Array): Promise<void>
  checkMissing(path: string): Promise<void>
  checkRegularFile(path: string): Promise<void>
  checkDirectory(path: string): Promise<void>
  checkSymlink(path: string, literalTarget: string): Promise<void>
  snapshot(paths: readonly string[]): Promise<FilesystemSnapshot>
  checkSnapshot(expected: FilesystemSnapshot): Promise<void>
}

export class DockerEnvironment implements Environment {
  private readonly container: StartedTestContainer
  readonly signal: AbortSignal

  constructor(container: StartedTestContainer, signal: AbortSignal) {
    this.container = container
    this.signal = signal
    registerDriverWriter(this, (path, bytes) => this.write(path, bytes, 0o755))
    registerDriverCleanup(this, (command, cleanupSignal) => this.execute(command, cleanupSignal))
  }

  async snapshot(paths: readonly string[]): Promise<FilesystemSnapshot> {
    this.signal.throwIfAborted()
    return captureSnapshot(this, paths)
  }

  async checkSnapshot(expected: FilesystemSnapshot): Promise<void> {
    const actual = await this.snapshot(expected.entries.map(({ path }) => path))
    const differences = compareSnapshots(expected, actual)
    if (differences.length) throw new Error(`Filesystem snapshot mismatch: ${JSON.stringify(differences)}`)
  }

  async run(command: Command): Promise<Result> {
    return this.execute(command, this.signal)
  }

  private async execute(command: Command, signal: AbortSignal): Promise<Result> {
    validateCommand(command)
    const env = Object.fromEntries(
      (command.env ?? []).map((entry) => {
        const separator = entry.indexOf('=')
        if (separator < 1) throw new TypeError('Environment entries must use NAME=value')
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
      if (!Number.isInteger(result.exitCode) || result.exitCode < 0)
        throw new DockerError('Testcontainers did not report an application exit code')
      return { exitCode: result.exitCode, stdout: result.stdout, stderr: result.stderr }
    } catch (cause) {
      // Testcontainers exposes output only after exec succeeds, never on rejection.
      throw new ExecutionError(
        { stdout: '', stderr: '' },
        signal.aborted ? signal.reason : cause instanceof DockerError ? cause : dockerFailure(cause),
      )
    }
  }

  async checkCommand(command: Command, expected: Result): Promise<void> {
    const actual = await this.run(command)
    if (
      actual.exitCode !== expected.exitCode ||
      actual.stdout !== expected.stdout ||
      actual.stderr !== expected.stderr
    ) {
      throw new Error(`Command result mismatch: actual=${JSON.stringify(actual)} expected=${JSON.stringify(expected)}`)
    }
  }

  async writeFile(path: string, bytes: Uint8Array): Promise<void> {
    return this.write(path, bytes, 0o644)
  }

  private async write(path: string, bytes: Uint8Array, mode: number): Promise<void> {
    pathValue(path)
    this.signal.throwIfAborted()
    if (!(await this.probe('-d', posix.dirname(path)))) throw new Error(`Parent directory does not exist: ${path}`)
    try {
      await untilCancelled(
        this.container.copyContentToContainer([{ target: path, content: Buffer.from(bytes), mode }]),
        this.signal,
      )
    } catch (cause) {
      throw this.signal.aborted ? this.signal.reason : dockerFailure(cause)
    }
  }

  async readFile(path: string): Promise<Buffer> {
    pathValue(path)
    this.signal.throwIfAborted()
    const copying = this.container.copyArchiveFromContainer(path)
    let archive: NodeJS.ReadableStream
    try {
      archive = await untilCancelled(copying, this.signal)
    } catch (cause) {
      // A response that arrives after cancellation is still ours to close.
      void copying
        .then((stream) => {
          if ('destroy' in stream && typeof stream.destroy === 'function') stream.destroy()
        })
        .catch(() => {})
      throw this.signal.aborted ? this.signal.reason : dockerFailure(cause)
    }
    const extract = tar.extract()
    const files: Buffer[] = []
    let invalid = false
    extract.on('entry', (header, stream, next) => {
      if (header.type !== 'file') invalid = true
      const chunks: Buffer[] = []
      stream.on('data', (chunk: unknown) => {
        if (!(chunk instanceof Uint8Array)) {
          extract.destroy(new DockerError('Expected archive byte stream'))
          return
        }
        chunks.push(Buffer.from(chunk))
      })
      stream.on('error', (error: Error) => extract.destroy(error))
      stream.on('end', () => {
        files.push(Buffer.concat(chunks))
        next()
      })
    })
    try {
      await pipeline(archive, extract, { signal: this.signal })
      if (invalid || files.length !== 1) throw new DockerError('Expected one regular file in Docker archive')
      return files[0]!
    } catch (cause) {
      throw this.signal.aborted ? this.signal.reason : cause
    }
  }

  async checkFile(path: string, expected: Uint8Array): Promise<void> {
    if (!(await this.readFile(path)).equals(Buffer.from(expected))) throw new Error(`File bytes mismatch: ${path}`)
  }

  private async probe(flag: string, path: string): Promise<boolean> {
    pathValue(path)
    const result = await this.run({ args: ['test', flag, path] })
    if (result.exitCode !== 0 && result.exitCode !== 1)
      throw new DockerError(`Path probe failed: ${JSON.stringify(result)}`)
    return result.exitCode === 0
  }

  async checkMissing(path: string): Promise<void> {
    if ((await this.probe('-e', path)) || (await this.probe('-L', path)))
      throw new Error(`Expected missing path: ${path}`)
  }
  private async physical(flag: string, path: string): Promise<void> {
    if (!(await this.probe(flag, path)) || (await this.probe('-L', path)))
      throw new Error(`Expected physical ${flag} path: ${path}`)
  }
  async checkRegularFile(path: string): Promise<void> {
    await this.physical('-f', path)
  }
  async checkDirectory(path: string): Promise<void> {
    await this.physical('-d', path)
  }
  async checkSymlink(path: string, literalTarget: string): Promise<void> {
    text(literalTarget, 'Symbolic link target')
    if (!(await this.probe('-L', path))) throw new Error(`Expected symbolic link: ${path}`)
    await this.checkCommand(
      { args: ['readlink', '--', path] },
      { exitCode: 0, stdout: `${literalTarget}\n`, stderr: '' },
    )
  }
}
