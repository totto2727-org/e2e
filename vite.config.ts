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
      'check:vp': { command: 'vp check', dependsOn: ['@totto2727/e2e#build'], cache: { output: [] } },
      'check:nixfmt': { command: 'nixfmt --check flake.nix', cache: { output: [] } },
      'fix:vp': { command: 'vp check --fix', dependsOn: ['@totto2727/e2e#fix'], cache: false },
      'fix:nixfmt': { command: 'nixfmt flake.nix', cache: false },
      check: {
        command: '',
        dependsOn: [
          'check:vp',
          'check:nixfmt',
          '@totto2727/e2e-go#check',
          '@totto2727/e2e-haskell#check',
          '@totto2727/e2e#check',
          '@totto2727/e2e-rust#check',
        ],
      },
      fix: {
        command: '',
        dependsOn: ['fix:vp', 'fix:nixfmt', '@totto2727/e2e-go#fix', '@totto2727/e2e-rust#fix'],
        cache: false,
      },
      build: {
        command: '',
        dependsOn: [
          '@totto2727/e2e-go#build',
          '@totto2727/e2e-haskell#build',
          '@totto2727/e2e#build',
          '@totto2727/e2e-rust#build',
        ],
      },
      test: {
        command: '',
        dependsOn: [
          '@totto2727/e2e-go#test',
          '@totto2727/e2e-haskell#test',
          '@totto2727/e2e#test',
          '@totto2727/e2e-rust#test',
        ],
      },
      pree2e: {
        command: 'docker build --tag e2e-example:local fixtures/cli',
        cache: false,
      },
      ci: { command: '', dependsOn: ['check', 'build', 'test', '@totto2727/e2e#postbuild'] },
      e2e: {
        command: "vp run --filter '!@totto2727/e2e-workspace' --concurrency-limit 1 e2e",
        dependsOn: ['pree2e'],
        cache: false,
      },
    },
  },
})
