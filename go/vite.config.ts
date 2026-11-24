import { defineConfig } from 'vite-plus'

export default defineConfig({
  run: {
    tasks: {
      build: { cache: true, command: 'just build', dependsOn: ['build:driver'] },
      'build:driver': { cache: true, command: 'just build-driver' },
      check: { cache: true, command: 'just check' },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
      e2e: { cache: false, command: 'just e2e' },
      fix: { cache: true, command: 'just fix' },
      test: { cache: true, command: 'just test' },
    },
  },
})
