import { randomUUID } from 'node:crypto'

import { GenericContainer, PullPolicy, log, type StartedTestContainer } from 'testcontainers'

import { untilCancelled } from './cancellation.js'
import { DockerEnvironment, type Environment } from './environment.js'
import { dockerFailure } from './errors.js'

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

async function stop(container: StartedTestContainer): Promise<void> {
  const cleanup = budget(30_000)
  try {
    // Testcontainers' timeout is the daemon's stop grace, not an HTTP deadline.
    await untilCancelled(container.stop({ timeout: 0, remove: true, removeVolumes: true }), cleanup.signal)
  } catch (cause) {
    throw cleanup.signal.aborted ? cleanup.signal.reason : dockerFailure(cause)
  } finally {
    cleanup.close()
  }
}

/** Run caller-owned test code in one fresh Testcontainers container. */
export async function withEnvironment<T>(
  image: string,
  action: (environment: Environment) => Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  const execution = budget(60_000, signal)
  let starting: Promise<StartedTestContainer> | undefined
  let container: StartedTestContainer | undefined
  const errors: unknown[] = []
  let result!: T
  try {
    execution.signal.throwIfAborted()
    if (typeof image !== 'string' || !image || image.includes('\0'))
      throw new TypeError('Image must be a nonempty NUL-free string')
    starting = new GenericContainer(image)
      .withLabels({ 'org.totto2727.e2e.owner': randomUUID() })
      .withPullPolicy(PullPolicy.neverPull())
      .withStartupTimeout(60_000)
      .start()
      .catch((cause: unknown) => {
        throw dockerFailure(cause)
      })
    container = await untilCancelled(starting, execution.signal)
    result = await action(new DockerEnvironment(container, execution.signal))
    execution.signal.throwIfAborted()
  } catch (error) {
    errors.push(error)
  } finally {
    execution.close()
    if (container) {
      try {
        await stop(container)
      } catch (error) {
        errors.push(error)
      }
    } else if (starting) {
      // Public start() cannot be cancelled. Remove a container that resolves late.
      // Rejected/partial starts remain under Testcontainers' resource reaper.
      void starting.then(
        async (late) => {
          try {
            await stop(late)
          } catch (error) {
            log.error(`Late-start container cleanup failed: ${String(error)}`, { containerId: late.getId() })
          }
        },
        () => {},
      )
    }
  }
  if (errors.length === 1) throw errors[0]
  if (errors.length) throw new AggregateError(errors, 'Environment and cleanup failed')
  return result
}
