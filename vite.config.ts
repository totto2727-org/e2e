import { fileURLToPath } from 'node:url'

import { generateIgnorePatterns } from '@totto2727/gitignore-patterns'
import { defineConfig } from 'vite-plus'

const ignorePatterns = await generateIgnorePatterns(new URL('.', import.meta.url))

export default defineConfig({
  fmt: {
    arrowParens: 'always',
    experimentalSortImports: { ignoreCase: true, newlinesBetween: true, order: 'asc' },
    experimentalSortPackageJson: true,
    jsxSingleQuote: true,
    printWidth: 120,
    proseWrap: 'preserve',
    semi: false,
    singleQuote: true,
    ignorePatterns,
  },
  lint: {
    ignorePatterns,
    jsPlugins: [fileURLToPath(import.meta.resolve('@totto2727/oxlint'))],
    options: { typeAware: true, typeCheck: true },
    rules: {
      'rules/no-eslint-disable-comments': 'error',
      'rules/no-jsx-script-tag': 'error',
      'rules/require-disable-reason': 'error',
      'rules/require-import-extension': 'error',
    },
  },
  run: {
    tasks: {
      'check:vp': { command: 'vp check', dependsOn: ['@totto2727/caseyard#build'], cache: true },
      'check:nixfmt': { command: 'nixfmt --check flake.nix', cache: true },
      'fix:vp': { command: 'vp check --fix', dependsOn: ['@totto2727/caseyard#fix'], cache: true },
      'fix:nixfmt': { command: 'nixfmt flake.nix', cache: true },
      check: {
        command: '',
        dependsOn: ['check:vp', 'check:nixfmt'],
      },
      fix: {
        command: '',
        dependsOn: ['fix:vp', 'fix:nixfmt'],
      },
      pree2e: {
        command:
          'docker build --tag e2e-example:local fixtures/cli && docker build --target nonroot --tag e2e-example-nonroot:local fixtures/cli',
        dependsOn: ['@totto2727/caseyard-go#build:driver'],
        cache: false,
      },
      ci: { command: '', dependsOn: ['check'] },
      e2e: {
        command: 'vp run -r --concurrency-limit 1 e2e',
        dependsOn: ['pree2e'],
        cache: false,
      },
    },
  },
})
