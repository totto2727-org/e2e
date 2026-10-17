export class DockerError extends Error {
  readonly statusCode: number | undefined

  constructor(message: string, statusCode?: number, options?: ErrorOptions) {
    super(message, options)
    this.statusCode = statusCode
    this.name = 'DockerError'
  }
}

export function dockerFailure(cause: unknown): DockerError {
  const statusCode =
    cause instanceof Error && 'statusCode' in cause && typeof cause.statusCode === 'number'
      ? cause.statusCode
      : undefined
  return new DockerError('Testcontainers operation failed', statusCode, { cause })
}
