import { defineConfig } from 'vite-plus'

export default defineConfig({
  fmt: {
    arrowParens: 'always',
    experimentalSortImports: {
      ignoreCase: true,
      newlinesBetween: true,
      order: 'asc',
    },
    experimentalSortPackageJson: true,
    jsxSingleQuote: true,
    printWidth: 120,
    proseWrap: 'preserve',
    semi: false,
    singleQuote: true,
  },
  lint: { options: { typeAware: true, typeCheck: true } },
  test: {
    project: ['native'],
    projects: [
      { test: { name: 'native', include: ['test/native.test.ts'] } },
      { test: { name: 'package', include: ['test/package.test.mjs'] } },
      { test: { name: 'docker', include: ['test/docker.test.ts'] } },
    ],
  },
  pack: {
    entry: ['src/index.ts', 'src/vitest.ts'],
    format: ['esm'],
    platform: 'node',
    dts: true,
    clean: true,
    deps: { neverBundle: ['vitest'] },
  },
  run: {
    tasks: {
      build: { command: '', dependsOn: ['build:vp'] },
      'build:vp': {
        command: 'vp pack',
        cache: {
          input: [{ auto: true }, '!dist/**'],
          output: ['dist/**'],
        },
      },
      check: { command: '', dependsOn: ['check:vp'] },
      'check:vp': { command: 'vp check', cache: { output: [] } },
      ci: { command: '', dependsOn: ['check', 'test', 'postbuild:pnpm'] },
      e2e: { command: '', dependsOn: ['e2e:vitest'], cache: false },
      'e2e:vitest': { command: 'vp test run --project docker', cache: false },
      fix: { command: '', dependsOn: ['fix:vp'], cache: false },
      'fix:vp': { command: 'vp check --fix', cache: false },
      'postbuild:pnpm': { command: 'vp pm pack -- --dry-run', dependsOn: ['build'], cache: { output: [] } },
      test: { command: '', dependsOn: ['test:vitest', 'test:vitest-package'] },
      'test:vitest': { command: 'vp test run --project native', cache: { output: [] } },
      'test:vitest-package': {
        command: 'vp test run --project package',
        dependsOn: ['build'],
        cache: { output: [] },
      },
    },
  },
})
