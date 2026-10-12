import http, { type IncomingMessage, type RequestOptions } from 'node:http'

export class DockerError extends Error {
  readonly statusCode: number | undefined

  constructor(message: string, statusCode?: number, options?: ErrorOptions) {
    super(message, options)
    this.statusCode = statusCode
    this.name = 'DockerError'
  }
}

export interface Transport {
  request(method: string, path: string, signal: AbortSignal, body?: Buffer): Promise<IncomingMessage>
}

export class Engine implements Transport {
  private readonly options: RequestOptions

  constructor(env: NodeJS.ProcessEnv = process.env) {
    if (env['DOCKER_TLS_VERIFY'] || env['DOCKER_CERT_PATH']) {
      throw new DockerError('Docker TLS configuration is unsupported')
    }
    const host = env['DOCKER_HOST'] ?? 'unix:///var/run/docker.sock'
    if (host.startsWith('unix://')) {
      const socketPath = host.slice(7)
      if (!socketPath || socketPath.includes('\0')) throw new DockerError('Invalid Docker Unix socket')
      this.options = { socketPath }
    } else {
      const url = new URL(host.replace(/^tcp:/, 'http:'))
      if (url.protocol !== 'http:' || url.username || url.password || url.pathname !== '/' || url.search || url.hash) {
        throw new DockerError('Only Unix sockets and explicit HTTP TCP Docker endpoints are supported')
      }
      this.options = { hostname: url.hostname, port: url.port || (host.startsWith('tcp:') ? 2375 : 80) }
    }
  }

  request(method: string, path: string, signal: AbortSignal, body?: Buffer): Promise<IncomingMessage> {
    return new Promise((resolve, reject) => {
      signal.throwIfAborted()
      const req = http.request({
        ...this.options,
        method,
        path,
        agent: false,
        headers: body
          ? {
              'Content-Type': path.includes('/archive?') ? 'application/x-tar' : 'application/json',
              'Content-Length': body.length,
            }
          : {},
      })
      let response: IncomingMessage | undefined
      const abort = () => {
        const error = signal.reason instanceof Error ? signal.reason : new Error('Operation cancelled')
        response?.destroy(error)
        req.destroy(error)
      }
      signal.addEventListener('abort', abort, { once: true })
      req.on('error', reject)
      req.on('upgrade', (_res, socket) => {
        socket.destroy()
        signal.removeEventListener('abort', abort)
        reject(new DockerError('Unexpected Docker HTTP upgrade: only 200 raw streams are supported'))
      })
      req.on('close', () => {
        if (!response) signal.removeEventListener('abort', abort)
      })
      req.on('response', (res) => {
        response = res
        res.on('close', () => signal.removeEventListener('abort', abort))
        resolve(res)
      })
      req.end(body)
    })
  }
}

export async function collect(stream: AsyncIterable<unknown>): Promise<Buffer> {
  const parts: Buffer[] = []
  for await (const chunk of stream) {
    if (!(chunk instanceof Uint8Array)) throw new DockerError('Expected byte stream chunk')
    parts.push(Buffer.from(chunk))
  }
  return Buffer.concat(parts)
}

export async function response(
  transport: Transport,
  method: string,
  path: string,
  signal: AbortSignal,
  body?: Buffer,
): Promise<IncomingMessage> {
  const res = await transport.request(method, path, signal, body)
  if (!res.statusCode || res.statusCode < 200 || res.statusCode >= 300) {
    const diagnostic = (await collect(res)).toString('utf8')
    throw new DockerError(`${method} ${path}: Docker HTTP ${res.statusCode}: ${diagnostic}`, res.statusCode)
  }
  return res
}

export async function json(
  transport: Transport,
  method: string,
  path: string,
  signal: AbortSignal,
  body?: object,
): Promise<unknown> {
  const res = await response(transport, method, path, signal, body ? Buffer.from(JSON.stringify(body)) : undefined)
  const bytes = await collect(res)
  return bytes.length ? JSON.parse(bytes.toString('utf8')) : undefined
}

export function stringField(value: unknown, key: string): string {
  if (
    !value ||
    typeof value !== 'object' ||
    !(key in value) ||
    typeof (value as Record<string, unknown>)[key] !== 'string' ||
    !(value as Record<string, unknown>)[key]
  ) {
    throw new DockerError(`Docker response missing ${key}`)
  }
  return (value as Record<string, string>)[key]!
}

export class Demultiplexer {
  private pending = Buffer.alloc(0)
  private remaining = 0
  private stream = 0
  private readonly stdout: Buffer[] = []
  private readonly stderr: Buffer[] = []

  push(chunk: Uint8Array): void {
    this.pending = Buffer.concat([this.pending, chunk])
    while (this.pending.length) {
      if (!this.remaining) {
        if (this.pending.length < 8) return
        this.stream = this.pending[0]!
        if ((this.stream !== 1 && this.stream !== 2) || this.pending.subarray(1, 4).some((byte) => byte !== 0)) {
          throw new DockerError('Invalid Docker multiplex stream header')
        }
        this.remaining = this.pending.readUInt32BE(4)
        this.pending = this.pending.subarray(8)
        if (!this.remaining) continue
      }
      const length = Math.min(this.remaining, this.pending.length)
      ;(this.stream === 1 ? this.stdout : this.stderr).push(Buffer.from(this.pending.subarray(0, length)))
      this.pending = this.pending.subarray(length)
      this.remaining -= length
    }
  }

  finish(): void {
    if (this.remaining || this.pending.length) throw new DockerError('Truncated Docker multiplex stream')
  }

  output(fatal = false): { stdout: string; stderr: string } {
    const decoder = new TextDecoder('utf-8', { fatal, ignoreBOM: true })
    return { stdout: decoder.decode(Buffer.concat(this.stdout)), stderr: decoder.decode(Buffer.concat(this.stderr)) }
  }
}
