import type { Command, Environment, Result } from './environment.js'

const installed = new WeakMap<Environment, string>()
const installing = new WeakSet<Environment>()
const writers = new WeakMap<Environment, (path: string, bytes: Uint8Array) => Promise<void>>()
const cleanupExecutors = new WeakMap<Environment, (command: Command, signal: AbortSignal) => Promise<Result>>()

export function registerDriverCleanup(
  environment: Environment,
  execute: (command: Command, signal: AbortSignal) => Promise<Result>,
): void {
  cleanupExecutors.set(environment, execute)
}

export function registerDriverWriter(
  environment: Environment,
  write: (path: string, bytes: Uint8Array) => Promise<void>,
): void {
  writers.set(environment, write)
}

/** Stage a caller-built Linux driver inside this environment. The container owns its lifetime. */
export async function installDriver(environment: Environment, executable: Uint8Array): Promise<void> {
  if (!(executable instanceof Uint8Array) || executable.length === 0)
    throw new TypeError('Driver executable must be nonempty bytes')
  environment.signal.throwIfAborted()
  if (installed.has(environment) || installing.has(environment))
    throw new Error('Driver already installed in this environment')
  const bytes = Buffer.from(executable)
  const write = writers.get(environment)
  if (!write) throw new TypeError('Driver installation requires a Caseyard environment')
  installing.add(environment)
  try {
    const directory = await environment.run({ args: ['mktemp', '-d', '/tmp/caseyard-driver-XXXXXXXXXX'] })
    const path = directory.stdout.trimEnd()
    if (directory.exitCode !== 0 || !/^\/tmp\/caseyard-driver-[A-Za-z0-9]+$/.test(path))
      throw new Error(`Driver staging failed: ${directory.stderr}`)
    const executablePath = `${path}/driver`
    await write(executablePath, bytes)
    installed.set(environment, executablePath)
  } finally {
    installing.delete(environment)
  }
}

export class DriverError extends Error {
  readonly observation: unknown

  constructor(message: string, observation: unknown) {
    super(message)
    this.observation = observation
  }
}

export function driverRecord(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('Invalid driver response')
  return value as Record<string, unknown>
}

export function driverBytes(value: unknown): Buffer {
  if (
    !Array.isArray(value) ||
    !value.every((item: unknown) => typeof item === 'number' && Number.isInteger(item) && item >= 0 && item <= 255)
  )
    throw new Error('Invalid driver byte array')
  return Buffer.from(value)
}

export async function invokeDriver(
  environment: Environment,
  mode: 'snapshot' | 'start' | 'request',
  request: object,
  command?: Command,
  cleanupSignal?: AbortSignal,
): Promise<unknown> {
  const signal = cleanupSignal ?? environment.signal
  signal.throwIfAborted()
  const executable = installed.get(environment)
  if (!executable) throw new Error('Call installDriver before using snapshots or terminals')
  const invocation: Command = {
    args: [executable, mode, JSON.stringify(request)],
    ...(command?.workingDir === undefined ? {} : { workingDir: command.workingDir }),
    ...(command?.env === undefined ? {} : { env: command.env }),
  }
  const cleanup = cleanupExecutors.get(environment)
  if (cleanupSignal && !cleanup) throw new Error('Missing driver cleanup executor')
  const result = cleanupSignal ? await cleanup!(invocation, cleanupSignal) : await environment.run(invocation)
  let decoded: unknown
  try {
    decoded = JSON.parse(result.stdout)
  } catch (cause) {
    throw new Error(`Invalid driver JSON: ${result.stderr}`, { cause })
  }
  const response = driverRecord(decoded)
  if (response['ok'] === false) {
    if (typeof response['error'] !== 'string') throw new Error('Invalid driver error')
    throw new DriverError(response['error'], response['observation'])
  }
  if (result.exitCode !== 0 || response['ok'] !== true || !('value' in response))
    throw new Error(`Driver failed: ${result.stderr}`)
  return response['value']
}
