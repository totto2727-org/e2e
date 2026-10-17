import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['multiple_scenarios.test.ts'],
    testTimeout: 125_000,
  },
})
