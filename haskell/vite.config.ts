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
      'check:cabal': { command: 'just check', cache: { input: inputs, output: ['tmp/cabal/**'] } },
      'build:cabal': {
        command: 'just build',
        dependsOn: ['check:cabal'],
        cache: { input: inputs, output: ['tmp/cabal/**'] },
      },
      'test:cabal': {
        command: 'just test',
        dependsOn: ['build:cabal'],
        cache: { input: inputs, output: ['tmp/cabal/**'] },
      },
      'e2e:cabal': { command: 'just e2e', cache: false },
      check: { command: '', dependsOn: ['check:cabal'] },
      build: { command: '', dependsOn: ['build:cabal'] },
      test: { command: '', dependsOn: ['test:cabal'] },
      e2e: { command: '', dependsOn: ['e2e:cabal'], cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
