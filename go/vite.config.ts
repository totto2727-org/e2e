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
      check: { command: 'just check', cache: { input: inputs, output: [] } },
      fix: { command: 'just fix', cache: false },
      build: { command: 'just build', cache: { input: inputs, output: [] } },
      test: { command: 'just test', cache: { input: inputs, output: [] } },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
