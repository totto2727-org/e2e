import { defineConfig } from 'vite-plus'

export default defineConfig({
  test: {
    project: ['native'],
    projects: [
      { test: { name: 'native', include: ['test/native.test.ts'] } },
      { test: { name: 'package', include: ['test/package.test.mjs'] } },
      { test: { name: 'docker', include: ['test/docker.test.ts'], testTimeout: 125_000 } },
    ],
  },
  pack: {
    entry: ['src/index.ts'],
    format: ['esm'],
    platform: 'node',
    dts: true,
    clean: true,
  },
  run: {
    tasks: {
      build: {
        command: 'vp pack',
        cache: true,
      },
      check: { command: 'vp check', dependsOn: ['build'], cache: true },
      ci: { command: '', dependsOn: ['check', 'test'] },
      e2e: { command: 'vp test run --project docker', cache: false },
      // Generate the shared public declarations before fixers start writing sources.
      fix: { command: 'vp check --fix', dependsOn: ['build'], cache: true },
      test: { command: '', dependsOn: ['test:vitest', 'test:vitest-package'] },
      'test:vitest': { command: 'vp test run --project native', cache: true },
      'test:vitest-package': {
        command: 'vp test run --project package',
        dependsOn: ['build'],
        cache: true,
      },
    },
  },
})
