# e2e

## Repository structure

```text
go/          Go module and independent Docker example module.
haskell/     Haskell library and Hspec tests.
typescript/  TypeScript library and Vitest integration.
rust/        Rust library and asynchronous tests.
```

The root README defines the language-independent contract and implementation status.
Each language README documents its concrete public API and supported Docker transports.
Keep language-specific implementation details out of the common contract.

## Development commands

- Run root tasks from the repository root.
- Enter the toolchain with `nix develop` or select a smaller shell with `nix develop .#go`, `.#haskell`, `.#typescript`, or `.#rust`.
- Install the root workspace with `vp install --frozen-lockfile`.
- Use Vite+ for cross-project dependencies and caching. Keep complex implementation-specific procedures in language-local Justfiles.
- Put lightweight commands directly in `vite.config.ts`; express multi-stage work with `dependsOn` instead of parallel task-runner hierarchies.
- `vp run fix` formats and autofixes supported implementations.
- `vp run check` checks formatting and lint findings.
- `vp run build` builds each implementation.
- `vp run test` runs tests that do not need Docker.
- `vp run ci` runs checks, builds, package validation, and native tests without Docker.
- `vp run e2e` is the explicit Docker acceptance entry point.
- Select one workspace task with its package name, such as `vp run @totto2727/e2e-rust#ci`.
- Keep native-only standalone workflows JavaScript-independent. When a project and every child have no JS dependencies and no useful cache-control need, use native commands for light tasks and local Just recipes for heavy procedures.
- Keep pure task caching enabled with complete source/configuration inputs and restorable outputs. Disable caching for mutations, image creation, and Docker acceptance.
- Select native and Docker tests through standard project, suite, package, or ignored-test command options, not environment-dependent discovery.
- Keep Docker tests out of the aggregate `ci` task.
- Keep temporary outputs under `tmp/` and do not commit them.
- Start from the corresponding workspace template and make only necessary library-specific extensions. There is no Haskell template in this workspace.

## Architecture and invariants

- Resolve a caller-built local image without pulling, then retain its immutable ID with a stopped lease container.
- Create a fresh container for each uniquely named case and remove all owned containers without deleting the image.
- Allow at most two concurrent cases per runner and retain the slot through cleanup.
- Start the one-minute case budget after acquiring a slot and before creating the container.
- Give cleanup a fresh thirty-second total budget, including retries.
- Pass literal argv entries and expose separate stdout, stderr, and inspected exit status.
- Distinguish command failure from Docker, transport, framing, and cancellation errors.
- Preserve observed partial output when reporting execution errors.
- Own attached stream connections and close them when the case is cancelled.
- Compare command results and file bytes exactly.
- Keep domain-specific workflows in consumer tests rather than the reusable libraries.
- Do not add compatibility adapters for retired APIs.
- Do not infer application exit status from a host `docker exec` process.
- Document transport differences instead of claiming unverified parity.

## Language-specific rules

- Go code lives in the `github.com/totto2727-org/e2e/go` module.
- The independent `go/example` module uses a local `replace` only for development.
- Check every Go error and use `%w` when adding context.
- Run Go tests with the race detector.
- Apply the `implement-cli-e2e` and `document-e2e-scenarios` skills to consumer Go scenarios.
- Keep sibling English scenario documents and validate them when changing scenario-bearing Go tests.
- TypeScript uses Node, pnpm with its authoritative lockfile, release-age safeguards, and Vitest integration.
- Keep native unit tests independent of Docker in every language.
- Before handoff, run the affected language tasks and the corresponding real Docker acceptance task when Docker validation is requested.
