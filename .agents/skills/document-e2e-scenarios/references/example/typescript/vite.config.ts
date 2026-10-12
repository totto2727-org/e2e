import { fileURLToPath } from 'node:url'

export default {
  test: {
    root: fileURLToPath(new URL('.', import.meta.url)),
    project: ['native'],
    projects: [
      { extends: true, test: { name: 'native', include: ['native/**/*.test.ts'] } },
      { extends: true, test: { name: 'docker', include: ['multiple_scenarios.test.ts'] } },
    ],
  },
  run: {
    tasks: {
      'check:tsc': { command: 'tsc --project tsconfig.json', dependsOn: ['@totto2727/e2e-cli#build'] },
      check: { command: '', dependsOn: ['check:tsc'] },
      ci: { command: '', dependsOn: ['check'] },
      'e2e:vitest': {
        command: 'vp test run --project docker',
        dependsOn: ['@totto2727/e2e-cli#build'],
        cache: false,
      },
      e2e: { command: '', dependsOn: ['e2e:vitest'], cache: false },
    },
  },
}
