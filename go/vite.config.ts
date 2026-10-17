import { defineConfig } from 'vite-plus'

export default defineConfig({
  run: {
    tasks: {
      check: { command: 'just check', cache: true },
      fix: { command: 'just fix', cache: true },
      build: { command: 'just build', cache: true },
      test: { command: 'just test', cache: true },
      e2e: { command: 'just e2e', cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
