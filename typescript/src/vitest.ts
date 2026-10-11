import { afterAll, beforeAll, describe, test } from 'vitest'

import { Suite, validateCases, type Case } from './runner.js'

/** Register named isolated cases without consuming their budget while queued. */
export function registerCases(image: string, cases: readonly Case[]): void {
  validateCases(cases)
  describe(`CLI ${image}`, () => {
    let suite: Suite
    beforeAll(async () => {
      suite = new Suite()
      await suite.open(image)
    }, 0)
    afterAll(async () => {
      await suite?.close()
    }, 0)
    for (const testCase of cases) {
      test.concurrent(
        testCase.name,
        async (context) => {
          await suite.run((environment) => testCase.run(environment), context.signal)
        },
        0,
      )
    }
  })
}
