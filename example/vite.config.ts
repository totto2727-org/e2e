import { defineConfig } from 'vite-plus'

export default defineConfig({
  run: {
    tasks: {
      build: {
        command: 'go build ./...',
      },
      check: {
        command: 'golangci-lint fmt --diff && golangci-lint run ./...',
      },
      fix: {
        command: 'golangci-lint fmt && golangci-lint run --fix ./...',
      },
    },
  },
})
