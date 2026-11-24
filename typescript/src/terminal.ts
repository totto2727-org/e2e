import { invokeDriver, DriverError, driverRecord as record, driverBytes as bytes } from './driver.ts'
import { validateCommand } from './environment.ts'
import type { Command, Environment } from './environment.ts'

export type TerminalChannel = 'stdout' | 'terminal'
export type TerminalSignal = 'SIGINT' | 'SIGTERM' | 'SIGHUP' | 'SIGQUIT' | 'SIGKILL' | 'SIGUSR1' | 'SIGUSR2'
export type TerminalStatus =
  | { readonly kind: 'running' }
  | { readonly kind: 'exited'; readonly code: number }
  | { readonly kind: 'signaled'; readonly signal: number }

export interface TerminalOutput {
  readonly stdout: Buffer
  readonly terminal: Buffer
}

export interface TerminalObservation extends TerminalOutput {
  readonly unreadInput: Buffer
  readonly status: TerminalStatus
  readonly termiosRestored: boolean
  readonly initialTermios: readonly (number | readonly number[])[]
  readonly currentTermios: readonly (number | readonly number[])[]
}

export interface TerminalOptions {
  readonly rows?: number
  readonly columns?: number
  /** Finite pipe input plus EOF. Omit to attach stdin to the terminal. */
  readonly input?: Uint8Array
  /** Seconds, positive and at most 60. Defaults to 50. */
  readonly timeout?: number
}

export interface TerminalSession {
  observe: () => Promise<TerminalObservation>
  expect: (channel: TerminalChannel, bytes: Uint8Array, timeout?: number) => Promise<TerminalObservation>
  send: (bytes: Uint8Array) => Promise<TerminalObservation>
  resize: (rows: number, columns: number) => Promise<TerminalObservation>
  signal: (name: TerminalSignal) => Promise<TerminalObservation>
  wait: (timeout?: number) => Promise<TerminalObservation>
}

export class TerminalError extends Error {
  readonly observation: TerminalOutput | TerminalObservation | undefined

  constructor(message: string, observation?: TerminalOutput | TerminalObservation) {
    super(message)
    this.name = 'TerminalError'
    this.observation = observation
  }
}

const termios = (value: unknown): readonly (number | readonly number[])[] => {
  if (!Array.isArray(value)) {
    throw new TerminalError('Invalid terminal settings')
  }
  return Object.freeze(
    value.map((item: unknown) => {
      if (typeof item === 'number' && Number.isInteger(item)) {
        return item
      }
      if (Array.isArray(item)) {
        return Object.freeze(
          item.map((part: unknown) => {
            if (typeof part !== 'number' || !Number.isInteger(part)) {
              throw new TerminalError('Invalid terminal settings')
            }
            return part
          }),
        )
      }
      throw new TerminalError('Invalid terminal settings')
    }),
  )
}

const terminalStatus = (status: Record<string, unknown>): TerminalStatus => {
  if (status['kind'] === 'running' && !('code' in status) && !('signal' in status)) {
    return { kind: 'running' }
  }
  if (
    status['kind'] === 'exited' &&
    !('signal' in status) &&
    typeof status['code'] === 'number' &&
    Number.isInteger(status['code']) &&
    status['code'] >= 0
  ) {
    return { code: status['code'], kind: 'exited' }
  }
  if (
    status['kind'] === 'signaled' &&
    !('code' in status) &&
    typeof status['signal'] === 'number' &&
    Number.isInteger(status['signal']) &&
    status['signal'] > 0
  ) {
    return { kind: 'signaled', signal: status['signal'] }
  }
  throw new TerminalError('Invalid terminal process status')
}

const observation = (value: unknown): TerminalObservation => {
  const data = record(value)
  const state = terminalStatus(record(data['status']))
  if (typeof data['termiosRestored'] !== 'boolean') {
    throw new TerminalError('Invalid terminal restoration flag')
  }
  return Object.freeze({
    currentTermios: termios(data['currentTermios']),
    initialTermios: termios(data['initialTermios']),
    status: Object.freeze(state),
    stdout: bytes(data['stdout']),
    terminal: bytes(data['terminal']),
    termiosRestored: data['termiosRestored'],
    unreadInput: bytes(data['unreadInput']),
  })
}

const duration = (value: number): number => {
  if (!Number.isFinite(value) || value <= 0 || value > 60) {
    throw new TypeError('Terminal timeout must be in (0, 60] seconds')
  }
  return value
}

const geometry = (rows: number, columns: number): void => {
  if (![rows, columns].every((value) => Number.isInteger(value) && value >= 1 && value <= 65_535)) {
    throw new TypeError('Terminal rows and columns must be integers in 1..65535')
  }
}

const invoke = async (
  environment: Environment,
  mode: 'start' | 'request',
  request: object,
  command?: Command,
  cleanupSignal?: AbortSignal,
): Promise<unknown> => {
  try {
    return await invokeDriver(environment, mode, request, command, cleanupSignal)
  } catch (error) {
    if (!(error instanceof DriverError)) {
      throw error
    }
    const raw = error.observation === null || error.observation === undefined ? undefined : record(error.observation)
    const captured =
      raw && ('status' in raw ? observation(raw) : { stdout: bytes(raw['stdout']), terminal: bytes(raw['terminal']) })
    throw new TerminalError(error.message, captured)
  }
}

const callbackValue = <T>(result: { value: T } | undefined): T => {
  if (result === undefined) {
    throw new Error('Terminal completed without a callback result')
  }
  return result.value
}

/** Own one Linux terminal session for the callback, then close and invalidate its handle. */
export const withTerminal = async <T>(
  environment: Environment,
  command: Command,
  options: TerminalOptions,
  action: (terminal: TerminalSession) => Promise<T>,
): Promise<T> => {
  validateCommand(command)
  // Validate environment entries before a session can be created.
  for (const entry of command.env ?? []) {
    if (entry.indexOf('=') < 1) {
      throw new TypeError('Environment entries must use NAME=value')
    }
  }
  const rows = options.rows ?? 40
  const columns = options.columns ?? 120
  geometry(rows, columns)
  const timeout = duration(options.timeout ?? 50)
  const pipeInput = options.input !== undefined
  if (options.input !== undefined && !(options.input instanceof Uint8Array)) {
    throw new TypeError('Terminal input must be bytes')
  }
  const started = record(
    await invoke(
      environment,
      'start',
      {
        argv: command.args,
        columns,
        input: options.input === undefined ? null : [...options.input],
        rows,
        timeout,
      },
      command,
    ),
  )
  const { path } = started
  if (
    typeof path !== 'string' ||
    !path.startsWith('/tmp/caseyard-pty-') ||
    !path.endsWith('/socket') ||
    path.includes('\0')
  ) {
    throw new TerminalError('Invalid terminal session path')
  }
  const lifecycle: { closed: boolean; result?: { value: T } } = { closed: false }
  const request = async (operation: string, fields: object = {}): Promise<TerminalObservation> => {
    if (lifecycle.closed) {
      throw new TerminalError('Terminal session is closed')
    }
    return observation(await invoke(environment, 'request', { operation, path, ...fields }))
  }
  const session: TerminalSession = Object.freeze({
    async expect(channel: TerminalChannel, data: Uint8Array, seconds = timeout) {
      if (channel !== 'stdout' && channel !== 'terminal') {
        return await Promise.reject(new TypeError('Invalid terminal channel'))
      }
      if (!(data instanceof Uint8Array) || !data.length) {
        return await Promise.reject(new TypeError('Expected nonempty bytes'))
      }
      return await request('expect', { channel, data: [...data], timeout: duration(seconds) })
    },
    observe: async () => await request('observe'),
    async resize(height: number, width: number) {
      geometry(height, width)
      return await request('resize', { columns: width, rows: height })
    },
    async send(data: Uint8Array) {
      if (!(data instanceof Uint8Array)) {
        return await Promise.reject(new TypeError('Terminal input must be bytes'))
      }
      if (pipeInput) {
        return await Promise.reject(new TypeError('Cannot send terminal input when stdin is a pipe'))
      }
      return await request('send', { data: [...data] })
    },
    async signal(name: TerminalSignal) {
      if (!['SIGINT', 'SIGTERM', 'SIGHUP', 'SIGQUIT', 'SIGKILL', 'SIGUSR1', 'SIGUSR2'].includes(name)) {
        return await Promise.reject(new TypeError('Unsupported terminal signal'))
      }
      return await request('signal', { name })
    },
    wait: async (seconds = timeout) => await request('wait', { timeout: duration(seconds) }),
  })
  const errors: unknown[] = []
  try {
    lifecycle.result = { value: await action(session) }
    environment.signal.throwIfAborted()
  } catch (error) {
    errors.push(error)
  }
  lifecycle.closed = true
  // Once the environment is cancelled, its container teardown is the final boundary.
  if (!environment.signal.aborted) {
    const cleanup = new AbortController()
    const timer = setTimeout(() => {
      cleanup.abort(new Error('Terminal cleanup deadline exceeded'))
    }, 30_000)
    try {
      const result = await invoke(environment, 'request', { operation: 'close', path }, undefined, cleanup.signal)
      if (result !== null) {
        throw new TerminalError('Invalid terminal close response')
      }
    } catch (error) {
      errors.push(error)
    } finally {
      clearTimeout(timer)
    }
  }
  if (errors.length === 1) {
    throw errors[0]
  }
  if (errors.length) {
    throw new AggregateError(errors, 'Terminal callback and cleanup failed')
  }
  return callbackValue(lifecycle.result)
}
