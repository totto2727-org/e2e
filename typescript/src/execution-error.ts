import type { PartialResult } from './environment.ts'

export class ExecutionError extends Error {
  readonly partialResult: PartialResult

  constructor(partialResult: PartialResult, cause: unknown) {
    super('Command execution failed', { cause })
    this.partialResult = partialResult
    this.name = 'ExecutionError'
  }
}
