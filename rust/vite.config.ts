import { defineConfig } from 'vite-plus'

const inputs = [
  'src/**',
  'tests/**',
  'Cargo.*',
  'rust-toolchain.toml',
  'Justfile',
  'vite.config.ts',
  'package.json',
  'README.md',
  { pattern: 'flake.*', base: 'workspace' as const },
]

export default defineConfig({
  run: {
    tasks: {
      'check:rustfmt': { command: 'just check-rustfmt', cache: { input: inputs, output: [] } },
      'check:clippy': {
        command: 'just check-clippy',
        dependsOn: ['check:rustfmt'],
        cache: { input: inputs, output: ['target/**'] },
      },
      'fix:rustfmt': { command: 'just fix-rustfmt', cache: false },
      'fix:clippy': { command: 'just fix-clippy', dependsOn: ['fix:rustfmt'], cache: false },
      // Cargo tasks share target/, including cache restoration. Keep them ordered.
      build: {
        command: 'just build',
        dependsOn: ['check:clippy'],
        cache: { input: inputs, output: ['target/**'] },
      },
      test: {
        command: 'just test',
        dependsOn: ['build'],
        cache: { input: inputs, output: ['target/**'] },
      },
      e2e: { command: 'just e2e', cache: false },
      check: { command: '', dependsOn: ['check:rustfmt', 'check:clippy'] },
      fix: { command: '', dependsOn: ['fix:rustfmt', 'fix:clippy'], cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
