import { randomUUID } from 'node:crypto'

import { DockerError, Engine, json, stringField, type Transport } from './engine.js'
import { DockerEnvironment, type Environment } from './environment.js'

export interface Case {
  name: string
  run(environment: Environment): Promise<void>
}
const ownershipLabel = 'org.totto2727.e2e.owner'

export class DeadlineError extends Error {
  constructor() {
    super('Case deadline exceeded')
    this.name = 'DeadlineError'
  }
}

function budget(milliseconds: number, parent?: AbortSignal): { signal: AbortSignal; close(): void } {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(new DeadlineError()), milliseconds)
  const cancel = () => controller.abort(parent?.reason)
  if (parent?.aborted) cancel()
  else parent?.addEventListener('abort', cancel, { once: true })
  return {
    signal: controller.signal,
    close() {
      clearTimeout(timer)
      parent?.removeEventListener('abort', cancel)
      controller.abort(new Error('Case completed'))
    },
  }
}

class Slots {
  private active = 0
  private readonly queue: (() => void)[] = []
  async acquire(signal?: AbortSignal): Promise<() => void> {
    signal?.throwIfAborted()
    if (this.active >= 2)
      await new Promise<void>((resolve, reject) => {
        const ready = () => {
          signal?.removeEventListener('abort', abort)
          resolve()
        }
        const abort = () => {
          const index = this.queue.indexOf(ready)
          if (index >= 0) this.queue.splice(index, 1)
          reject(signal?.reason)
        }
        signal?.addEventListener('abort', abort, { once: true })
        this.queue.push(ready)
      })
    // A released slot is reserved for the first waiter before it resumes.
    else this.active++
    if (signal?.aborted) {
      this.release()
      signal.throwIfAborted()
    }
    return () => this.release()
  }
  private release(): void {
    const next = this.queue.shift()
    if (next) next()
    else this.active--
  }
}

interface Owned {
  name: string
  token: string
}
function ownership(): Owned {
  const token = randomUUID()
  return { name: `e2e-ts-${token}`, token }
}

export class Suite {
  private readonly slots = new Slots()
  private imageID = ''
  private lease: Owned | undefined
  private readonly transport: Transport
  constructor(transport: Transport = new Engine()) {
    this.transport = transport
  }

  async open(image: string): Promise<void> {
    const setup = budget(60_000)
    try {
      this.imageID = stringField(
        await json(this.transport, 'GET', `/images/${encodeURIComponent(image)}/json`, setup.signal),
        'Id',
      )
      this.lease = ownership()
      await this.create(this.lease, setup.signal)
    } catch (error) {
      try {
        await this.close()
      } catch (cleanup) {
        throw new AggregateError([error, cleanup], 'Image lease setup and cleanup failed')
      }
      throw error
    } finally {
      setup.close()
    }
  }

  private async create(owned: Owned, signal: AbortSignal): Promise<string> {
    return stringField(
      await json(this.transport, 'POST', `/containers/create?name=${encodeURIComponent(owned.name)}`, signal, {
        Image: this.imageID,
        Labels: { [ownershipLabel]: owned.token },
      }),
      'Id',
    )
  }

  private async remove(owned: Owned): Promise<void> {
    const cleanup = budget(30_000)
    try {
      let inspected: unknown
      try {
        inspected = await json(this.transport, 'GET', `/containers/${owned.name}/json`, cleanup.signal)
      } catch (error) {
        if (error instanceof DockerError && error.statusCode === 404) return
        throw error
      }
      if (!inspected || typeof inspected !== 'object' || !('Config' in inspected))
        throw new DockerError('Invalid container ownership inspection')
      const config = inspected.Config
      if (
        !config ||
        typeof config !== 'object' ||
        !('Labels' in config) ||
        !config.Labels ||
        typeof config.Labels !== 'object' ||
        !(ownershipLabel in config.Labels) ||
        config.Labels[ownershipLabel] !== owned.token
      ) {
        throw new DockerError(`Refusing to remove unowned container ${owned.name}`)
      }
      try {
        await json(this.transport, 'DELETE', `/containers/${owned.name}?force=true&v=true`, cleanup.signal)
      } catch (error) {
        if (!(error instanceof DockerError && error.statusCode === 404)) throw error
      }
    } finally {
      cleanup.close()
    }
  }

  async run(action: Case['run'], parent?: AbortSignal): Promise<void> {
    const release = await this.slots.acquire(parent)
    const execution = budget(60_000, parent)
    const owned = ownership()
    const errors: unknown[] = []
    try {
      if (!this.imageID) throw new Error('Suite must be opened before running cases')
      const id = await this.create(owned, execution.signal)
      await json(this.transport, 'POST', `/containers/${encodeURIComponent(id)}/start`, execution.signal)
      await action(new DockerEnvironment(this.transport, id, execution.signal))
    } catch (error) {
      errors.push(error)
    } finally {
      execution.close()
      try {
        await this.remove(owned)
      } catch (error) {
        errors.push(error)
      } finally {
        release()
      }
    }
    if (errors.length === 1) throw errors[0]
    if (errors.length) throw new AggregateError(errors, 'Case and cleanup failed')
  }

  async close(): Promise<void> {
    if (this.lease) {
      const lease = this.lease
      this.lease = undefined
      await this.remove(lease)
    }
  }
}

export async function runCases(image: string, cases: readonly Case[], signal?: AbortSignal): Promise<void> {
  validateCases(cases)
  const suite = new Suite()
  await suite.open(image)
  const errors: unknown[] = []
  try {
    const results = await Promise.allSettled(
      cases.map((testCase) => suite.run((environment) => testCase.run(environment), signal)),
    )
    for (const result of results) if (result.status === 'rejected') errors.push(result.reason)
  } finally {
    try {
      await suite.close()
    } catch (error) {
      errors.push(error)
    }
  }
  if (errors.length) throw new AggregateError(errors, 'Cases failed')
}

export function validateCases(cases: readonly Case[]): void {
  const names = new Set<string>()
  for (const testCase of cases) {
    if (!testCase.name || names.has(testCase.name)) throw new TypeError('Cases require unique nonempty names')
    names.add(testCase.name)
  }
}
