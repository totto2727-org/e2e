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
    options: { typeAware: true, typeCheck: true },
    ignorePatterns: [
      ...ignorePatterns,
      // This documentation snippet uses dependencies supplied by the consumer's project.
      '.agents/skills/cli-e2e/references/example/typescript/**',
    ],
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
        command: 'docker build --tag e2e-example:local fixtures/cli',
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
