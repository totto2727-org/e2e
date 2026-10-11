# Haskell Docker acceptance scenarios

This reference serves contributors who run or change `Spec.hs`.
Build `e2e-example:local` before you start the explicit `e2e` task.
The shared [Go fixture Dockerfile](../../go/example/Dockerfile) supplies `/workspace`, Linux command tools, and a running `sleep infinity` entrypoint.
The Haskell library does not build or pull the fixture.

| Named case | Commands and options | Expected observation |
| --- | --- | --- |
| `greeting` | `echo`, argv value `hello from e2e` | Code `0`, stdout `hello from e2e\n`, empty stderr. |
| `file workflow` | Explicit `sh -c` writes `first`, then appends `second`; `cat /workspace/result.txt`; `stat -c %a /workspace/bytes` | Commands share one case container. The persisted file equals `first\nsecond\n`. Archive transfer preserves bytes `00 ff 0a` and mode `0644`. |
| `expected failure` | Explicit `sh -c` prints `expected-failure`, then `exit 7` | Code `7` is a successful command result with exact stdout and empty stderr. |
| `separate streams` | Explicit `sh -c`, working directory `/workspace`, environment entry `HOME=/workspace/synthetic-home` | Code `7`, stdout `/workspace/synthetic-home:/workspace\n`, stderr `diagnostic\n`. Both `run` and `checkCommand` agree. |
| `physical paths` | Write fixture file; `mkdir`; `ln -s` creates file, dangling, and directory links; assertion commands use `test` and `readlink --` | Physical file and directory checks succeed. Missing paths are accepted. Literal relative and dangling link targets are preserved. A dangling link is not missing. File and directory links are not physical paths. A resolved link target is rejected. |
| `deadline` | `sleep infinity` | `DeadlineExceeded`, not a result. The command returns within 70 seconds and uses approximately the 60-second absolute case budget. |

Each case records its read-only container ID.
The audit requires six distinct case IDs.
Before and after the run, the test queries all containers with `org.e2e.haskell.owner`.
The sets must be equal, so no new case or stopped lease remains.
The local image ID must also be unchanged.
These audit reads use a private test-only Engine adapter; no Docker escape hatch is part of the public API.

The executable reports every named case through Hspec after the shared runner completes.
The audit has three additional Hspec examples for identities, cleanup, and image preservation.
Do not run another Haskell Docker acceptance invocation concurrently because the label-set audit assumes exclusive ownership of that test label during its snapshot window.
Native tests in `../test/Spec.hs` use controlled HTTP and socket fixtures and do not establish real Docker acceptance.
