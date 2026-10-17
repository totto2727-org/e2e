import { defineConfig } from 'vite-plus'

// Cargo reads and updates target/; keep it out of the source fingerprint.
const cache = {
  env: [
    'NIX_CFLAGS_COMPILE',
    'NIX_LDFLAGS',
    'PKG_CONFIG_PATH',
    'SDKROOT',
    'MACOSX_DEPLOYMENT_TARGET',
    ...Object.keys(process.env).filter((name) => /^NIX_(CC|BINTOOLS)_WRAPPER_TARGET_HOST_/.test(name)),
  ],
  input: [{ auto: true }, '!target/**'],
  output: ['target/**'],
}

export default defineConfig({
  run: {
    tasks: {
      'check:rustfmt': { command: 'just check-rustfmt', cache: true },
      'check:clippy': {
        command: 'just check-clippy',
        dependsOn: ['check:rustfmt'],
        cache,
      },
      'fix:rustfmt': { command: 'just fix-rustfmt', cache: true },
      'fix:clippy': { command: 'just fix-clippy', dependsOn: ['fix:rustfmt'], cache },
      // Cargo tasks share target/, including cache restoration. Keep them ordered.
      build: {
        command: 'just build',
        dependsOn: ['check:clippy'],
        cache,
      },
      test: {
        command: 'just test',
        dependsOn: ['build'],
        cache,
      },
      e2e: { command: 'just e2e', cache: false },
      check: { command: '', dependsOn: ['check:rustfmt', 'check:clippy'] },
      fix: { command: '', dependsOn: ['fix:rustfmt', 'fix:clippy'] },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
