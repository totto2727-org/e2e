import { defineConfig } from 'vite-plus'

export default defineConfig({
  run: {
    tasks: {
      check: { command: 'just check', cache: true },
      fix: { command: 'just fix', cache: true },
      'build:driver': { command: 'just build-driver', cache: true },
      build: { command: 'just build', dependsOn: ['build:driver'], cache: true },
      test: { command: 'just test', cache: true },
      e2e: { command: 'just e2e', cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
