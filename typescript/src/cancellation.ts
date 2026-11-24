/** Bound caller waiting without pretending to cancel Testcontainers' underlying request. */
export const untilCancelled = async <T>(operation: Promise<T>, signal: AbortSignal): Promise<T> => {
  const { promise: cancelled, reject } = Promise.withResolvers<never>()
  const abort = (): void => {
    reject(signal.reason)
  }
  if (signal.aborted) {
    abort()
  } else {
    signal.addEventListener('abort', abort, { once: true })
  }
  try {
    return await Promise.race([operation, cancelled])
  } finally {
    signal.removeEventListener('abort', abort)
  }
}
