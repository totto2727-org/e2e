import { defineConfig } from 'vite-plus'

const inputs = [
  '*.go',
  'go.mod',
  'go.sum',
  'Justfile',
  'vite.config.ts',
  'package.json',
  { pattern: 'go/.golangci.yml', base: 'workspace' as const },
  { pattern: 'go/cli/**', base: 'workspace' as const },
  { pattern: 'go/go.mod', base: 'workspace' as const },
  { pattern: 'go/go.sum', base: 'workspace' as const },
  { pattern: 'flake.*', base: 'workspace' as const },
]

export default defineConfig({
  run: {
    tasks: {
      check: { command: 'just check', cache: { input: inputs, output: [] } },
      fix: { command: 'just fix', cache: false },
      build: { command: 'just build', cache: { input: inputs, output: ['tmp/build/**'] } },
      test: { command: 'just test', cache: { input: inputs, output: [] } },
      pree2e: { command: 'just image', cache: false },
      e2e: { command: 'just e2e-test', cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
