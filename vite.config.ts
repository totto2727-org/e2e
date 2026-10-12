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
  lint: { options: { typeAware: true, typeCheck: true }, ignorePatterns },
  run: {
    tasks: {
      'check:vp': { command: 'vp check', cache: { output: [] } },
      'check:nixfmt': { command: 'nixfmt --check flake.nix', cache: { output: [] } },
      'fix:vp': { command: 'vp check --fix', dependsOn: ['@totto2727/e2e-cli#fix'], cache: false },
      'fix:nixfmt': { command: 'nixfmt flake.nix', cache: false },
      check: {
        command: '',
        dependsOn: [
          'check:vp',
          'check:nixfmt',
          'check:go',
          '@totto2727/e2e-doc-examples#check',
          '@totto2727/e2e-go#check',
          '@totto2727/e2e-go-example#check',
          '@totto2727/e2e-haskell#check',
          '@totto2727/e2e-cli#check',
          '@totto2727/e2e-rust#check',
        ],
      },
      fix: {
        command: '',
        dependsOn: [
          'fix:vp',
          'fix:nixfmt',
          '@totto2727/e2e-go#fix',
          '@totto2727/e2e-go-example#fix',
          '@totto2727/e2e-rust#fix',
        ],
        cache: false,
      },
      build: {
        command: '',
        dependsOn: [
          '@totto2727/e2e-go#build',
          '@totto2727/e2e-go-example#build',
          '@totto2727/e2e-haskell#build',
          '@totto2727/e2e-cli#build',
          '@totto2727/e2e-rust#build',
          'build:cabal',
          'build:cargo',
        ],
      },
      test: {
        command: '',
        dependsOn: [
          '@totto2727/e2e-go#test',
          '@totto2727/e2e-go-example#test',
          '@totto2727/e2e-haskell#test',
          '@totto2727/e2e-cli#test',
          '@totto2727/e2e-rust#test',
        ],
      },
      'build:cabal': {
        command: 'just build',
        cwd: '.agents/skills/document-e2e-scenarios/references/example/haskell',
        cache: {
          input: [
            '.agents/skills/document-e2e-scenarios/references/example/haskell/**',
            'haskell/src/**',
            'haskell/*.cabal',
            'flake.*',
          ],
          output: ['tmp/scenario-doc-examples/haskell/**'],
        },
      },
      'build:cargo': {
        command: 'just build',
        cwd: '.agents/skills/document-e2e-scenarios/references/example/rust',
        cache: {
          input: [
            '.agents/skills/document-e2e-scenarios/references/example/rust/**',
            'rust/src/**',
            'rust/Cargo.*',
            'rust/rust-toolchain.toml',
            'flake.*',
          ],
          output: ['tmp/scenario-doc-examples/rust/**'],
        },
      },
      'check:go': {
        command:
          "go test -race -run '^$' ../.agents/skills/document-e2e-scenarios/references/example/multiple_scenarios_test.go",
        cwd: 'go',
        cache: {
          input: [
            'go/cli/**',
            'go/go.*',
            '.agents/skills/document-e2e-scenarios/references/example/multiple_scenarios_test.go',
            'flake.*',
          ],
          output: [],
        },
      },
      pree2e: {
        command: 'docker tag e2e-example:local example-cli:local',
        dependsOn: ['@totto2727/e2e-go-example#pree2e'],
        cache: false,
      },
      'e2e:vp': {
        command: "vp run --filter '!@totto2727/e2e' --concurrency-limit 1 e2e",
        dependsOn: ['pree2e'],
        cache: false,
      },
      'e2e:go': {
        command:
          'go test -race -v ../.agents/skills/document-e2e-scenarios/references/example/multiple_scenarios_test.go',
        cwd: 'go',
        dependsOn: ['e2e:vp'],
        cache: false,
      },
      'e2e:cabal': {
        command: 'just e2e',
        cwd: '.agents/skills/document-e2e-scenarios/references/example/haskell',
        dependsOn: ['e2e:go'],
        cache: false,
      },
      'e2e:cargo': {
        command: 'just e2e',
        cwd: '.agents/skills/document-e2e-scenarios/references/example/rust',
        dependsOn: ['e2e:cabal'],
        cache: false,
      },
      ci: { command: '', dependsOn: ['check', 'build', 'test', '@totto2727/e2e-cli#postbuild'] },
      e2e: { command: '', dependsOn: ['e2e:cargo'], cache: false },
    },
  },
})
