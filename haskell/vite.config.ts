import { defineConfig } from 'vite-plus'

// Cabal's C dependencies use the native search paths supplied by the Nix shell.
const env = ['NIX_CFLAGS_COMPILE', 'NIX_LDFLAGS', 'PKG_CONFIG_PATH', 'SDKROOT', 'MACOSX_DEPLOYMENT_TARGET']
// Cabal reads and updates its build state; do not fingerprint it as source input.
const cache = { env, input: [{ auto: true }, '!dist-newstyle/**'], output: ['dist-newstyle/**'] }

export default defineConfig({
  run: {
    // Keep tasks ordered by dependencies where they share mutable build state.
    tasks: {
      build: {
        cache,
        command: 'just build',
        dependsOn: ['check'],
      },
      check: { cache, command: 'just check' },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
      e2e: { cache: false, command: 'just e2e' },
      test: {
        cache,
        command: 'just test',
        dependsOn: ['build'],
      },
    },
  },
})
