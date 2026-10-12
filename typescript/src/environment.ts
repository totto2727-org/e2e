import { posix } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

import tar from 'tar-stream'

import { collect, Demultiplexer, DockerError, json, response, stringField, type Transport } from './engine.js'

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
}

export class DockerEnvironment implements Environment {
  private readonly transport: Transport
  private readonly container: string
  readonly signal: AbortSignal

  constructor(transport: Transport, container: string, signal: AbortSignal) {
    this.transport = transport
    this.container = container
    this.signal = signal
  }

  async run(command: Command): Promise<Result> {
    validateCommand(command)
    const decoder = new Demultiplexer()
    try {
      this.signal.throwIfAborted()
      const exec = stringField(
        await json(this.transport, 'POST', `/containers/${encodeURIComponent(this.container)}/exec`, this.signal, {
          AttachStdout: true,
          AttachStderr: true,
          Tty: false,
          Cmd: command.args,
          WorkingDir: command.workingDir ?? '',
          Env: command.env ?? [],
        }),
        'Id',
      )
      const stream = await response(
        this.transport,
        'POST',
        `/exec/${encodeURIComponent(exec)}/start`,
        this.signal,
        Buffer.from(JSON.stringify({ Detach: false, Tty: false })),
      )
      try {
        for await (const chunk of stream) decoder.push(chunk as Uint8Array)
        decoder.finish()
      } finally {
        stream.destroy()
      }
      for (;;) {
        const inspect = await json(this.transport, 'GET', `/exec/${encodeURIComponent(exec)}/json`, this.signal)
        if (
          !inspect ||
          typeof inspect !== 'object' ||
          !('Running' in inspect) ||
          typeof inspect.Running !== 'boolean'
        ) {
          throw new DockerError('Invalid Docker exec inspection')
        }
        if (inspect.Running) {
          await delay(100, undefined, { signal: this.signal })
          continue
        }
        if (!('ExitCode' in inspect) || !Number.isInteger(inspect.ExitCode))
          throw new DockerError('Invalid Docker exit code')
        return { exitCode: inspect.ExitCode as number, ...decoder.output(true) }
      }
    } catch (cause) {
      throw new ExecutionError(decoder.output(), this.signal.aborted ? this.signal.reason : cause)
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
    pathValue(path)
    this.signal.throwIfAborted()
    const pack = tar.pack()
    const collected = collect(pack)
    pack.entry({ name: posix.basename(path), mode: 0o644, type: 'file' }, Buffer.from(bytes), (error) => {
      if (error) pack.destroy(error)
      else pack.finalize()
    })
    const archive = await collected
    const res = await response(
      this.transport,
      'PUT',
      `/containers/${encodeURIComponent(this.container)}/archive?path=${encodeURIComponent(posix.dirname(path))}`,
      this.signal,
      archive,
    )
    await collect(res)
  }

  async readFile(path: string): Promise<Buffer> {
    pathValue(path)
    const res = await response(
      this.transport,
      'GET',
      `/containers/${encodeURIComponent(this.container)}/archive?path=${encodeURIComponent(path)}`,
      this.signal,
    )
    const archive = await collect(res)
    this.signal.throwIfAborted()
    return new Promise((resolve, reject) => {
      const extract = tar.extract()
      const files: Buffer[] = []
      let invalid = false
      extract.on('entry', (header, stream, next) => {
        if (header.type !== 'file') invalid = true
        void collect(stream).then(
          (bytes) => {
            files.push(bytes)
            next()
          },
          (error: unknown) => extract.destroy(error instanceof Error ? error : new Error(String(error))),
        )
      })
      extract.on('error', reject)
      extract.on('finish', () => {
        if (invalid || files.length !== 1) reject(new DockerError('Expected one regular file in Docker archive'))
        else resolve(files[0]!)
      })
      extract.end(archive)
    })
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
