import { defineConfig } from 'vite-plus'

// Cargo reads and updates target/; keep it out of the source fingerprint.
const cache = {
  env: [
    'NIX_CFLAGS_COMPILE',
    'NIX_LDFLAGS',
    'PKG_CONFIG_PATH',
    'SDKROOT',
    'MACOSX_DEPLOYMENT_TARGET',
    ...Object.keys(process.env).filter((name) => /^NIX_(?:CC|BINTOOLS)_WRAPPER_TARGET_HOST_/u.test(name)),
  ],
  input: [{ auto: true }, '!target/**'],
  output: ['target/**'],
}

export default defineConfig({
  run: {
    // Keep tasks ordered by dependencies where they share mutable build state.
    tasks: {
      build: {
        cache,
        command: 'just build',
        dependsOn: ['check:clippy'],
      },
      check: { command: '', dependsOn: ['check:rustfmt', 'check:clippy'] },
      'check:clippy': {
        cache,
        command: 'just check-clippy',
        dependsOn: ['check:rustfmt'],
      },
      'check:rustfmt': { cache: true, command: 'just check-rustfmt' },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
      e2e: { cache: false, command: 'just e2e' },
      fix: { command: '', dependsOn: ['fix:rustfmt', 'fix:clippy'] },
      'fix:clippy': { cache, command: 'just fix-clippy', dependsOn: ['fix:rustfmt'] },
      'fix:rustfmt': { cache: true, command: 'just fix-rustfmt' },
      test: {
        cache,
        command: 'just test',
        dependsOn: ['build'],
      },
    },
  },
})
