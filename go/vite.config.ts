import { defineConfig } from 'vite-plus'

const inputs = [
  'cli/**',
  'go.mod',
  'go.sum',
  '.golangci.yml',
  'Justfile',
  'vite.config.ts',
  'package.json',
  { pattern: 'flake.*', base: 'workspace' as const },
]

export default defineConfig({
  run: {
    tasks: {
      'check:golangci-lint': { command: 'just check', cache: { input: inputs, output: [] } },
      'fix:golangci-lint': { command: 'just fix', cache: false },
      'build:go': { command: 'just build', cache: { input: inputs, output: [] } },
      'test:go': { command: 'just test', cache: { input: inputs, output: [] } },
      check: { command: '', dependsOn: ['check:golangci-lint'] },
      fix: { command: '', dependsOn: ['fix:golangci-lint'], cache: false },
      build: { command: '', dependsOn: ['build:go'] },
      test: { command: '', dependsOn: ['test:go'] },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
