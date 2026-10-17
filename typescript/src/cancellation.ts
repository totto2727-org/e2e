/** Bound caller waiting without pretending to cancel Testcontainers' underlying request. */
export async function untilCancelled<T>(operation: Promise<T>, signal: AbortSignal): Promise<T> {
  let abort!: () => void
  const cancelled = new Promise<never>((_resolve, reject) => {
    abort = () => reject(signal.reason)
    if (signal.aborted) abort()
    else signal.addEventListener('abort', abort, { once: true })
  })
  try {
    return await Promise.race([operation, cancelled])
  } finally {
    signal.removeEventListener('abort', abort)
  }
}
