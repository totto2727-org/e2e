import { defineConfig } from 'vite-plus'

// Cabal's C dependencies use the native search paths supplied by the Nix shell.
const env = ['NIX_CFLAGS_COMPILE', 'NIX_LDFLAGS', 'PKG_CONFIG_PATH', 'SDKROOT', 'MACOSX_DEPLOYMENT_TARGET']
// Cabal reads and updates its build state; do not fingerprint it as source input.
const cache = { env, input: [{ auto: true }, '!dist-newstyle/**'], output: ['dist-newstyle/**'] }

export default defineConfig({
  run: {
    tasks: {
      // Cabal planning and cache restoration share dist-newstyle/. Keep them ordered.
      check: { command: 'just check', cache },
      build: {
        command: 'just build',
        dependsOn: ['check'],
        cache,
      },
      test: {
        command: 'just test',
        dependsOn: ['build'],
        cache,
      },
      e2e: { command: 'just e2e', cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
