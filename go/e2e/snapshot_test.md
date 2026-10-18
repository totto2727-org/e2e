# Filesystem snapshot scenarios

Source: [snapshot_test.go](./snapshot_test.go)

This document is for contributors who maintain Go Docker acceptance tests.
Use the [repository validation workflow](../../AGENTS.md#validation) to build the fixture images and static driver artifacts.
The tests select driver bytes with the container's `uname -m`, then call `InstallDriver` once per environment.
No Python or Go installation is required in either application image.

## `TestFilesystemSnapshotScenario`

Capture explicit paths for a missing path, binary regular files, physical directories, a file link, and a dangling link with a newline in its literal target.
Require unchanged state before mutation.
Create and delete paths, change one binary byte, replace a file with a directory, and change the dangling target.
Retarget a link from `c bytes` to `./c bytes` and verify its literal target even though both targets resolve to the same file.
Change JSON whitespace and trailing newline without changing its object values and verify the exact new bytes.
Capture again twice and compare complete sorted `Entries()` with `slices.Equal` and native `testing` diagnostics.
Changes to unselected directory children and permissions do not change captured entries.

## `TestFilesystemSnapshotRejectsUnsupportedKindScenario`

Capture an absent path, then create a FIFO at that path.
Capture must return an error instead of accepting the unsupported kind.

## `TestFilesystemSnapshotPermissionErrorsScenario`

Use `e2e-example-nonroot:local` to capture a missing path, then remove search permission from its newly created parent directory.
Capture must reject both the inaccessible descendant and a physical file with no read permission.
