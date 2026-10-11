# e2e

Small libraries for CLI end-to-end tests in disposable Linux containers.
The shared contract is language-independent. Each implementation uses its language's normal values, errors, and test framework.

## Purpose and boundaries

Build the application image outside the test, then exercise the real executable through argv commands and observable output or filesystem state.
Every case owns a fresh container. Multi-command workflows remain in the test, not in the library.
The libraries do not build, pull, or remove the caller's application image, mount the host workspace, run interactive commands, or manage application-specific state.
An implementation may use backend infrastructure images, such as Testcontainers' resource reaper; its language README documents this behavior.
A container is not a shell abstraction: commands are argv entries, and shell syntax requires an explicit shell command.

## Implementations

| Language | Implementation and API | Framework | Status |
| --- | --- | --- | --- |
| Go | [go/README.md](./go/README.md) | `testing` | API simplified; integration validation in progress. |
| Haskell | [haskell/README.md](./haskell/README.md) | Hspec | Planned. |
| TypeScript | [typescript/README.md](./typescript/README.md) | Vitest | Planned. |
| Rust | [rust/README.md](./rust/README.md) | Tokio tests | Planned. |

Status must distinguish implementation, native checks, and real Docker acceptance. A mocked transport test is not Docker acceptance.
No package publication or release is implied by this table.

## Shared values

- **Case**: a unique name and an action that receives an isolated environment.
- **Command**: nonempty argv with a nonempty executable, an optional working directory, and optional environment entries. Preserve argument boundaries, empty argument values, and environment values. Reject NUL in argv, working directory, and environment entries before execution.
- **Result**: exit code, stdout, and stderr. Keep both output streams separate and unchanged. Preserve ordering within each stream, but do not promise ordering between streams. Acceptance examples use UTF-8 text; file APIs transfer exact bytes.
- **Environment**: the case-owned command and file operations. Do not expose a mutable Docker client as a public escape hatch.

A nonzero application exit code is a command result, not an infrastructure error.
Daemon failures, failure to attach, output decode errors, cancellation, and deadline expiry are errors. An exit code is valid only after successful execution and inspection.
Execution errors retain output already captured when the language API can carry it. Each language README explains that error representation.
Never infer application exit status solely from the host `docker exec` process: its failure can instead be a Docker error.

## Shared operations

Names below identify behaviors, not mandatory spellings in every language.

| Operation | Contract |
| --- | --- |
| Run cases | Resolve a local prebuilt image once and run each case in a fresh container. |
| Run command | Execute a `Command` and return a separate-stream `Result`. |
| Check command | Execute a command and compare its exit code, stdout, and stderr exactly against an expected `Result`. An empty expected stream asserts absence, not an ignored field. |
| Write file | Copy exact bytes to a container path with mode `0644`. The parent directory must exist. |
| Read file | Return exact bytes from a container file. |
| Check file | Read a file and compare exact bytes. |
| Check missing | Reject any existing path, including a dangling symbolic link. |
| Check regular file | Require a physical regular file, not a symbolic link to a file. |
| Check directory | Require a physical directory, not a symbolic link to a directory. |
| Check symbolic link | Compare the literal target, without resolving or normalizing it. A dangling target is valid. |

Path assertions require `test` with `-e`, `-L`, `-f`, and `-d`, and `readlink` with `--` support in the image.
Probe exit codes other than `0` or `1` are infrastructure errors, not successful absence checks.

## Container lifecycle and deadlines

1. Inspect the caller's local image. Fail if it is absent, without pulling it.
2. Resolve the immutable image ID and create a stopped lease container from that ID. Do not delete the image.
3. Acquire a case slot. At most two cases may own slots concurrently; queued cases have not started their execution budget.
4. Start a one-minute case budget, including container creation and startup. Create a fresh container from the resolved image ID and preserve its image entrypoint.
5. Register cleanup immediately when ownership is acquired, including partial startup failures. Do not let a command connection outlive its case.
6. Execute all case operations against that case's deadline. Deadline expiry or cancellation must unblock output reads and release their owned connections.
7. On completion, failure, or cancellation, remove the case container using a fresh 30-second cleanup budget independent of the case deadline. Release its slot only after cleanup.
8. After all cases finish, remove the stopped lease with its own fresh 30-second budget. Preserve the caller-owned image.

The supplied image must remain running long enough to receive commands; the acceptance fixture uses `sleep infinity`.
There is no generic application-readiness check.
Closing an exec connection does not prove that the remote process stopped. Container cleanup is the required final process boundary.
Report cleanup failure instead of silently treating a leaked resource as success.
Use unpredictable names and ownership labels to recover owned resources when creation fails before returning an ID.
Cleanup cannot guarantee recovery if the daemon commits a creation only after a failed request and the subsequent cleanup lookup have both completed; daemon outages and unresolved creation outcomes must be reported rather than hidden.
These budgets bound library operations, not arbitrary caller code. Each language documents the effect of its framework's cancellation mechanism.

The baseline is a local Linux-container Docker Engine. Backend choice is not part of API equivalence: Testcontainers, a native Docker client, or a small Engine API adapter must satisfy the same observable contract.
A backend must not silently pull images or merge infrastructure diagnostics into command stderr.
Additional remote-daemon, TLS, or platform support must be documented and tested per implementation, not assumed from another language.

## Shared acceptance behavior

Each language must exercise its public interface against the same caller-built image and report these observations:

| Case | Observable behavior |
| --- | --- |
| Greeting | Exact stdout, empty stderr, and exit code zero. |
| File workflow | Multiple argv commands share one case container; copied bytes equal the persisted file. |
| Expected failure | Exit code `7` is a successful execution result, not an infrastructure error. |
| Separate streams | Working directory and environment reach the process; stdout and stderr remain distinct on a nonzero result. |
| Physical paths | Regular files, directories, relative and dangling links, and missing paths follow the exact assertion contracts; invalid states are rejected. |
| Deadline | An indefinitely running command returns a deadline error; cleanup removes the container. |

Also verify distinct container identities for separate cases, no scenario or lease containers left behind, and preservation of the caller-owned image.
Native tests should cover invalid input, infrastructure errors, exact comparison failures, cleanup failures, and cancellation at controlled transport boundaries without requiring Docker.
Keep Docker execution behind an explicit E2E task. Normal build and unit-test tasks must not require Docker.

## Repository and tasks

`go/`, `haskell/`, `typescript/`, and `rust/` contain independently usable libraries and their language-specific API documentation.
The root Justfile is the shared entrypoint for checks and explicit Docker acceptance.
See [AGENTS.md](./AGENTS.md) for contributor instructions and the language READMEs for concrete commands.

## License

No license has been declared for this repository.
