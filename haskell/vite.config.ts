import { defineConfig } from 'vite-plus'

const inputs = [
  'src/**',
  'test/**',
  'e2e/**',
  '*.cabal',
  'cabal.project',
  'Justfile',
  'vite.config.ts',
  'package.json',
  'README.md',
  { pattern: 'flake.*', base: 'workspace' as const },
]

// Cabal's C dependencies use the native search paths supplied by the Nix shell.
const env = ['NIX_CFLAGS_COMPILE', 'NIX_LDFLAGS', 'PKG_CONFIG_PATH', 'SDKROOT', 'MACOSX_DEPLOYMENT_TARGET']

export default defineConfig({
  run: {
    tasks: {
      // Cabal planning and cache restoration share dist-newstyle/. Keep them ordered.
      check: { command: 'just check', cache: { input: inputs, env, output: ['dist-newstyle/**'] } },
      build: {
        command: 'just build',
        dependsOn: ['check'],
        cache: { input: inputs, env, output: ['dist-newstyle/**'] },
      },
      test: {
        command: 'just test',
        dependsOn: ['build'],
        cache: { input: inputs, env, output: ['dist-newstyle/**'] },
      },
      e2e: { command: 'just e2e', cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
