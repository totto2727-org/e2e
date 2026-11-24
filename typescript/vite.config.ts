import preset from '@totto2727/oxlint/typescript'
import { defineConfig } from 'vite-plus'

export default defineConfig({
  lint: {
    ...preset,
    options: { typeAware: true, typeCheck: true },
  },
  pack: {
    clean: true,
    dts: true,
    entry: ['src/index.ts'],
    format: ['esm'],
    platform: 'node',
  },
  run: {
    tasks: {
      build: {
        cache: true,
        command: 'vp pack',
      },
      check: { cache: true, command: 'vp check', dependsOn: ['build'] },
      ci: { command: '', dependsOn: ['check', 'test'] },
      e2e: { cache: false, command: 'vp test run --project docker' },
      // Generate the shared public declarations before fixers start writing sources.
      fix: { cache: true, command: 'vp check --fix', dependsOn: ['build'] },
      test: { command: '', dependsOn: ['test:vitest', 'test:vitest-package'] },
      'test:vitest': { cache: true, command: 'vp test run --project native' },
      'test:vitest-package': {
        cache: true,
        command: 'vp test run --project package',
        dependsOn: ['build'],
      },
    },
  },
  test: {
    project: ['native'],
    projects: [
      { test: { include: ['test/native.test.ts'], name: 'native' } },
      { test: { include: ['test/package.test.mjs'], name: 'package' } },
      { test: { include: ['test/docker.test.ts'], name: 'docker', testTimeout: 125_000 } },
    ],
  },
})
