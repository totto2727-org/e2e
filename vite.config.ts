import { generateIgnorePatterns } from '@totto2727/gitignore-patterns'
import preset from '@totto2727/oxlint/typescript'
import { defineConfig } from 'vite-plus'

const ignorePatterns = await generateIgnorePatterns(new URL('.', import.meta.url))

export default defineConfig({
  fmt: {
    arrowParens: 'always',
    experimentalSortImports: { ignoreCase: true, newlinesBetween: true, order: 'asc' },
    experimentalSortPackageJson: true,
    ignorePatterns,
    jsxSingleQuote: true,
    printWidth: 120,
    proseWrap: 'preserve',
    semi: false,
    singleQuote: true,
  },
  lint: {
    ...preset,
    ignorePatterns: [...(preset.ignorePatterns ?? []), ...ignorePatterns],
    options: { typeAware: true, typeCheck: true },
  },
  run: {
    tasks: {
      check: {
        command: '',
        dependsOn: ['check:vp', 'check:nixfmt'],
      },
      'check:nixfmt': { cache: true, command: 'nixfmt --check flake.nix' },
      'check:vp': { cache: true, command: 'vp check', dependsOn: ['@totto2727/caseyard#build'] },
      ci: { command: '', dependsOn: ['check'] },
      e2e: {
        cache: false,
        command: 'vp run -r --concurrency-limit 1 e2e',
        dependsOn: ['pree2e'],
      },
      fix: {
        command: '',
        dependsOn: ['fix:vp', 'fix:nixfmt'],
      },
      'fix:nixfmt': { cache: true, command: 'nixfmt flake.nix' },
      'fix:vp': { cache: true, command: 'vp check --fix', dependsOn: ['@totto2727/caseyard#fix'] },
      pree2e: {
        cache: false,
        command:
          'docker build --tag e2e-example:local fixtures/cli && docker build --target nonroot --tag e2e-example-nonroot:local fixtures/cli',
        dependsOn: ['@totto2727/caseyard-go#build:driver'],
      },
    },
  },
})
