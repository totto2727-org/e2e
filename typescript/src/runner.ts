import { randomUUID } from 'node:crypto'

import { DockerError, Engine, json, stringField, type Transport } from './engine.js'
import { DockerEnvironment, type Environment } from './environment.js'

const ownershipLabel = 'org.totto2727.e2e.owner'

export class DeadlineError extends Error {
  constructor() {
    super('Environment deadline exceeded')
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
      controller.abort(new Error('Environment completed'))
    },
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

/** Internal transport seam for native tests, not a package export. */
export class Lifecycle {
  private readonly transport: Transport

  constructor(transport: Transport) {
    this.transport = transport
  }

  private async create(owned: Owned, imageID: string, signal: AbortSignal): Promise<string> {
    return stringField(
      await json(this.transport, 'POST', `/containers/create?name=${encodeURIComponent(owned.name)}`, signal, {
        Image: imageID,
        Labels: { [ownershipLabel]: owned.token },
      }),
      'Id',
    )
  }

  private async remove(owned: Owned, signal: AbortSignal): Promise<void> {
    let inspected: unknown
    try {
      inspected = await json(this.transport, 'GET', `/containers/${owned.name}/json`, signal)
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
      await json(this.transport, 'DELETE', `/containers/${owned.name}?force=true&v=true`, signal)
    } catch (error) {
      if (!(error instanceof DockerError && error.statusCode === 404)) throw error
    }
  }

  async run<T>(image: string, action: (environment: Environment) => Promise<T>, parent?: AbortSignal): Promise<T> {
    const execution = budget(60_000, parent)
    const owned: Owned[] = []
    const errors: unknown[] = []
    let result!: T
    try {
      execution.signal.throwIfAborted()
      const imageID = stringField(
        await json(this.transport, 'GET', `/images/${encodeURIComponent(image)}/json`, execution.signal),
        'Id',
      )
      const lease = ownership()
      owned.push(lease)
      await this.create(lease, imageID, execution.signal)
      const container = ownership()
      owned.push(container)
      const id = await this.create(container, imageID, execution.signal)
      await json(this.transport, 'POST', `/containers/${encodeURIComponent(id)}/start`, execution.signal)
      result = await action(new DockerEnvironment(this.transport, id, execution.signal))
      execution.signal.throwIfAborted()
    } catch (error) {
      errors.push(error)
    } finally {
      execution.close()
      for (const container of owned.reverse()) {
        const cleanup = budget(30_000)
        try {
          await this.remove(container, cleanup.signal)
        } catch (error) {
          errors.push(error)
        } finally {
          cleanup.close()
        }
      }
    }
    if (errors.length === 1) throw errors[0]
    if (errors.length) throw new AggregateError(errors, 'Environment and cleanup failed')
    return result
  }
}

/** Run caller-owned test code in one fresh container, then release all owned resources. */
export async function withEnvironment<T>(
  image: string,
  action: (environment: Environment) => Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  return new Lifecycle(new Engine()).run(image, action, signal)
}
