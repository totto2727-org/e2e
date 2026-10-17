import { defineConfig } from 'vite-plus'

export default defineConfig({
  run: {
    tasks: {
      check: { command: 'just check', cache: { output: [] } },
      fix: { command: 'just fix', cache: false },
      build: { command: 'just build', cache: { output: [] } },
      test: { command: 'just test', cache: { output: [] } },
      e2e: { command: 'just e2e', cache: false },
      ci: { command: '', dependsOn: ['check', 'build', 'test'] },
    },
  },
})
