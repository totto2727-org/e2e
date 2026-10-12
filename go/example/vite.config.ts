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
      'check:golangci-lint': { command: 'just check', cache: { input: inputs, output: [] } },
      'fix:golangci-lint': { command: 'just fix', cache: false },
      'build:go': { command: 'just build', cache: { input: inputs, output: ['tmp/build/**'] } },
      'test:go': { command: 'just test', cache: { input: inputs, output: [] } },
      'pree2e:docker': { command: 'just image', cache: false },
      'e2e:go': { command: 'just e2e-test', cache: false },
      check: { command: '', dependsOn: ['check:golangci-lint'] },
      fix: { command: '', dependsOn: ['fix:golangci-lint'], cache: false },
      build: { command: '', dependsOn: ['build:go'] },
      test: { command: '', dependsOn: ['test:go'] },
      pree2e: { command: '', dependsOn: ['pree2e:docker'], cache: false },
      e2e: { command: '', dependsOn: ['e2e:go'], cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
