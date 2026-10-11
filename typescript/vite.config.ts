import { defineConfig } from 'vite-plus'

export default defineConfig({
  fmt: { semi: false, singleQuote: true, printWidth: 120, proseWrap: 'preserve' },
  lint: { options: { typeAware: true, typeCheck: true } },
  test: {
    include:
      process.env.E2E_DOCKER === '1' ? ['test/docker.test.ts'] : ['test/native.test.ts', 'test/package.test.mjs'],
  },
  pack: {
    entry: ['src/index.ts', 'src/vitest.ts'],
    format: ['esm'],
    platform: 'node',
    dts: true,
    clean: true,
    deps: { neverBundle: ['vitest'] },
  },
  run: {
    tasks: {
      check: { command: 'vp check' },
      fix: { command: 'vp check --fix' },
      build: { command: 'vp pack' },
      test: { command: 'vp test run' },
      e2e: { command: 'E2E_DOCKER=1 vp test run', cache: false },
    },
  },
})
