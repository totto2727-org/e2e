import { randomUUID } from 'node:crypto'

import { GenericContainer, PullPolicy, log } from 'testcontainers'
import type { StartedTestContainer } from 'testcontainers'

import { untilCancelled } from './cancellation.ts'
import { DockerEnvironment } from './environment.ts'
import type { Environment } from './environment.ts'
import { dockerFailure } from './errors.ts'

export class DeadlineError extends Error {
  constructor() {
    super('Environment deadline exceeded')
    this.name = 'DeadlineError'
  }
}

const budget = (milliseconds: number, parent?: AbortSignal): { signal: AbortSignal; close: () => void } => {
  const controller = new AbortController()
  const timer = setTimeout(() => {
    controller.abort(new DeadlineError())
  }, milliseconds)
  const cancel = () => {
    controller.abort(parent?.reason)
  }
  if (parent?.aborted === true) {
    cancel()
  } else {
    parent?.addEventListener('abort', cancel, { once: true })
  }
  return {
    close() {
      clearTimeout(timer)
      parent?.removeEventListener('abort', cancel)
      controller.abort(new Error('Environment completed'))
    },
    signal: controller.signal,
  }
}

const stop = async (container: StartedTestContainer): Promise<void> => {
  const cleanup = budget(30_000)
  try {
    // Testcontainers' timeout is the daemon's stop grace, not an HTTP deadline.
    await untilCancelled(container.stop({ remove: true, removeVolumes: true, timeout: 0 }), cleanup.signal)
  } catch (error) {
    throw cleanup.signal.aborted ? cleanup.signal.reason : dockerFailure(error)
  } finally {
    cleanup.close()
  }
}

const start = async (image: string): Promise<StartedTestContainer> => {
  try {
    return await new GenericContainer(image)
      .withLabels({ 'org.totto2727.e2e.owner': randomUUID() })
      .withPullPolicy(PullPolicy.neverPull())
      .withStartupTimeout(60_000)
      .start()
  } catch (error) {
    throw dockerFailure(error)
  }
}

const stopLate = async (starting: Promise<StartedTestContainer>): Promise<void> => {
  try {
    const late = await starting
    try {
      await stop(late)
    } catch (error) {
      log.error(`Late-start container cleanup failed: ${String(error)}`, { containerId: late.getId() })
    }
  } catch {
    // Rejected or partial starts remain under Testcontainers' resource reaper.
  }
}

/** Run caller-owned test code in one fresh Testcontainers container. */
export const withEnvironment = async <T>(
  image: string,
  action: (environment: Environment) => Promise<T>,
  signal?: AbortSignal,
): Promise<T> => {
  const execution = budget(60_000, signal)
  const lifecycle: {
    starting?: Promise<StartedTestContainer>
    container?: StartedTestContainer
    result?: { value: T }
  } = {}
  const errors: unknown[] = []
  try {
    execution.signal.throwIfAborted()
    if (typeof image !== 'string' || !image || image.includes('\0')) {
      throw new TypeError('Image must be a nonempty NUL-free string')
    }
    lifecycle.starting = start(image)
    lifecycle.container = await untilCancelled(lifecycle.starting, execution.signal)
    lifecycle.result = { value: await action(new DockerEnvironment(lifecycle.container, execution.signal)) }
    execution.signal.throwIfAborted()
  } catch (error) {
    errors.push(error)
  } finally {
    execution.close()
    if (lifecycle.container) {
      try {
        await stop(lifecycle.container)
      } catch (error) {
        errors.push(error)
      }
    } else if (lifecycle.starting) {
      // The public start() operation can resolve after the caller is cancelled.
      void stopLate(lifecycle.starting)
    }
  }
  if (errors.length === 1) {
    throw errors[0]
  }
  if (errors.length) {
    throw new AggregateError(errors, 'Environment and cleanup failed')
  }
  if (lifecycle.result === undefined) {
    throw new Error('Environment completed without a callback result')
  }
  return lifecycle.result.value
}
