import { defineConfig } from 'vite-plus'

const inputs = [
  'src/**',
  'test/**',
  'e2e/**',
  '*.cabal',
  'Justfile',
  'vite.config.ts',
  'package.json',
  'README.md',
  { pattern: 'flake.*', base: 'workspace' as const },
]

export default defineConfig({
  run: {
    tasks: {
      // Cabal planning and cache restoration share tmp/cabal/. Keep them ordered.
      check: { command: 'just check', cache: { input: inputs, output: ['tmp/cabal/**'] } },
      build: {
        command: 'just build',
        dependsOn: ['check'],
        cache: { input: inputs, output: ['tmp/cabal/**'] },
      },
      test: {
        command: 'just test',
        dependsOn: ['build'],
        cache: { input: inputs, output: ['tmp/cabal/**'] },
      },
      e2e: { command: 'just e2e', cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
