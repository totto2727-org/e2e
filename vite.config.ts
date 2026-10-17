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
      'check:vp': { command: 'vp check', dependsOn: ['@totto2727/caseyard#build'], cache: { output: [] } },
      'check:nixfmt': { command: 'nixfmt --check flake.nix', cache: { output: [] } },
      'fix:vp': { command: 'vp check --fix', dependsOn: ['@totto2727/caseyard#fix'], cache: false },
      'fix:nixfmt': { command: 'nixfmt flake.nix', cache: false },
      check: {
        command: '',
        dependsOn: [
          'check:vp',
          'check:nixfmt',
          '@totto2727/caseyard-go#check',
          '@totto2727/caseyard-haskell#check',
          '@totto2727/caseyard#check',
          '@totto2727/caseyard-rust#check',
        ],
      },
      fix: {
        command: '',
        dependsOn: ['fix:vp', 'fix:nixfmt', '@totto2727/caseyard-go#fix', '@totto2727/caseyard-rust#fix'],
        cache: false,
      },
      build: {
        command: '',
        dependsOn: [
          '@totto2727/caseyard-go#build',
          '@totto2727/caseyard-haskell#build',
          '@totto2727/caseyard#build',
          '@totto2727/caseyard-rust#build',
        ],
      },
      test: {
        command: '',
        dependsOn: [
          '@totto2727/caseyard-go#test',
          '@totto2727/caseyard-haskell#test',
          '@totto2727/caseyard#test',
          '@totto2727/caseyard-rust#test',
        ],
      },
      pree2e: {
        command: 'docker build --tag e2e-example:local fixtures/cli',
        cache: false,
      },
      ci: { command: '', dependsOn: ['check', 'build', 'test', '@totto2727/caseyard#postbuild'] },
      e2e: {
        command: "vp run --filter '!@totto2727/caseyard-workspace' --concurrency-limit 1 e2e",
        dependsOn: ['pree2e'],
        cache: false,
      },
    },
  },
})
