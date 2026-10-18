import { invokeDriver, DriverError, driverRecord as record, driverBytes as bytes } from './driver.js'
import { validateCommand, type Command, type Environment } from './environment.js'

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
  observe(): Promise<TerminalObservation>
  expect(channel: TerminalChannel, bytes: Uint8Array, timeout?: number): Promise<TerminalObservation>
  send(bytes: Uint8Array): Promise<TerminalObservation>
  resize(rows: number, columns: number): Promise<TerminalObservation>
  signal(name: TerminalSignal): Promise<TerminalObservation>
  wait(timeout?: number): Promise<TerminalObservation>
}

export class TerminalError extends Error {
  readonly observation: TerminalOutput | TerminalObservation | undefined

  constructor(message: string, observation?: TerminalOutput | TerminalObservation) {
    super(message)
    this.name = 'TerminalError'
    this.observation = observation
  }
}

function termios(value: unknown): readonly (number | readonly number[])[] {
  if (!Array.isArray(value)) throw new TerminalError('Invalid terminal settings')
  return Object.freeze(
    value.map((item: unknown) => {
      if (typeof item === 'number' && Number.isInteger(item)) return item
      if (Array.isArray(item) && item.every((part: unknown) => typeof part === 'number' && Number.isInteger(part)))
        return Object.freeze([...item] as number[])
      throw new TerminalError('Invalid terminal settings')
    }),
  )
}

function observation(value: unknown): TerminalObservation {
  const data = record(value)
  const status = record(data['status'])
  let state: TerminalStatus
  if (status['kind'] === 'running' && !('code' in status) && !('signal' in status)) state = { kind: 'running' }
  else if (
    status['kind'] === 'exited' &&
    !('signal' in status) &&
    typeof status['code'] === 'number' &&
    Number.isInteger(status['code']) &&
    status['code'] >= 0
  )
    state = { kind: 'exited', code: status['code'] }
  else if (
    status['kind'] === 'signaled' &&
    !('code' in status) &&
    typeof status['signal'] === 'number' &&
    Number.isInteger(status['signal']) &&
    status['signal'] > 0
  )
    state = { kind: 'signaled', signal: status['signal'] }
  else throw new TerminalError('Invalid terminal process status')
  if (typeof data['termiosRestored'] !== 'boolean') throw new TerminalError('Invalid terminal restoration flag')
  return Object.freeze({
    stdout: bytes(data['stdout']),
    terminal: bytes(data['terminal']),
    unreadInput: bytes(data['unreadInput']),
    status: Object.freeze(state),
    termiosRestored: data['termiosRestored'],
    initialTermios: termios(data['initialTermios']),
    currentTermios: termios(data['currentTermios']),
  })
}

function duration(value: number): number {
  if (!Number.isFinite(value) || value <= 0 || value > 60)
    throw new TypeError('Terminal timeout must be in (0, 60] seconds')
  return value
}

function geometry(rows: number, columns: number): void {
  if (![rows, columns].every((value) => Number.isInteger(value) && value >= 1 && value <= 65535))
    throw new TypeError('Terminal rows and columns must be integers in 1..65535')
}

async function invoke(
  environment: Environment,
  mode: 'start' | 'request',
  request: object,
  command?: Command,
  cleanupSignal?: AbortSignal,
): Promise<unknown> {
  try {
    return await invokeDriver(environment, mode, request, command, cleanupSignal)
  } catch (cause) {
    if (!(cause instanceof DriverError)) throw cause
    let captured: TerminalOutput | TerminalObservation | undefined
    if (cause.observation !== null && cause.observation !== undefined) {
      const raw = record(cause.observation)
      captured = 'status' in raw ? observation(raw) : { stdout: bytes(raw['stdout']), terminal: bytes(raw['terminal']) }
    }
    throw new TerminalError(cause.message, captured)
  }
}

/** Own one Linux terminal session for the callback, then close and invalidate its handle. */
export async function withTerminal<T>(
  environment: Environment,
  command: Command,
  options: TerminalOptions,
  action: (terminal: TerminalSession) => Promise<T>,
): Promise<T> {
  validateCommand(command)
  // Validate environment entries before a session can be created.
  for (const entry of command.env ?? [])
    if (entry.indexOf('=') < 1) throw new TypeError('Environment entries must use NAME=value')
  const rows = options.rows ?? 40
  const columns = options.columns ?? 120
  geometry(rows, columns)
  const timeout = duration(options.timeout ?? 50)
  const pipeInput = options.input !== undefined
  if (options.input !== undefined && !(options.input instanceof Uint8Array))
    throw new TypeError('Terminal input must be bytes')
  const started = record(
    await invoke(
      environment,
      'start',
      {
        argv: command.args,
        rows,
        columns,
        timeout,
        input: options.input === undefined ? null : [...options.input],
      },
      command,
    ),
  )
  const path = started['path']
  if (
    typeof path !== 'string' ||
    !path.startsWith('/tmp/caseyard-pty-') ||
    !path.endsWith('/socket') ||
    path.includes('\0')
  )
    throw new TerminalError('Invalid terminal session path')
  let closed = false
  const request = async (operation: string, fields: object = {}): Promise<TerminalObservation> => {
    if (closed) throw new TerminalError('Terminal session is closed')
    return observation(await invoke(environment, 'request', { path, operation, ...fields }))
  }
  const session: TerminalSession = Object.freeze({
    observe: () => request('observe'),
    async expect(channel: TerminalChannel, data: Uint8Array, seconds = timeout) {
      if (channel !== 'stdout' && channel !== 'terminal')
        return Promise.reject(new TypeError('Invalid terminal channel'))
      if (!(data instanceof Uint8Array) || !data.length) return Promise.reject(new TypeError('Expected nonempty bytes'))
      return request('expect', { channel, data: [...data], timeout: duration(seconds) })
    },
    async send(data: Uint8Array) {
      if (!(data instanceof Uint8Array)) return Promise.reject(new TypeError('Terminal input must be bytes'))
      if (pipeInput) return Promise.reject(new TypeError('Cannot send terminal input when stdin is a pipe'))
      return request('send', { data: [...data] })
    },
    async resize(height: number, width: number) {
      geometry(height, width)
      return request('resize', { rows: height, columns: width })
    },
    async signal(name: TerminalSignal) {
      if (!['SIGINT', 'SIGTERM', 'SIGHUP', 'SIGQUIT', 'SIGKILL', 'SIGUSR1', 'SIGUSR2'].includes(name))
        return Promise.reject(new TypeError('Unsupported terminal signal'))
      return request('signal', { name })
    },
    wait: async (seconds = timeout) => request('wait', { timeout: duration(seconds) }),
  })
  const errors: unknown[] = []
  let value!: T
  try {
    value = await action(session)
    environment.signal.throwIfAborted()
  } catch (cause) {
    errors.push(cause)
  }
  closed = true
  // Once the environment is cancelled, its container teardown is the final boundary.
  if (!environment.signal.aborted) {
    const cleanup = new AbortController()
    const timer = setTimeout(() => cleanup.abort(new Error('Terminal cleanup deadline exceeded')), 30_000)
    try {
      const result = await invoke(environment, 'request', { path, operation: 'close' }, undefined, cleanup.signal)
      if (result !== null) throw new TerminalError('Invalid terminal close response')
    } catch (cause) {
      errors.push(cause)
    } finally {
      clearTimeout(timer)
    }
  }
  if (errors.length === 1) throw errors[0]
  if (errors.length) throw new AggregateError(errors, 'Terminal callback and cleanup failed')
  return value
}
