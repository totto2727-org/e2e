# Go CLI E2E template

This Testcontainers template demonstrates the reusable `github.com/totto2727-org/e2e/cli` package. The E2E helper module and this example are separate Go modules. Running the example tests requires Docker and fails rather than skips when Docker is unavailable.

Run these commands from the repository root:

```sh
just check
just build
just test
just e2e
```

The example intentionally keeps Docker execution behind the explicit `e2e` recipe so reusable package tests do not start Docker.

For maintenance after editing dependencies or Go files, run `just fix` and run `go mod tidy` in both module directories.

`cli.Run` builds one uniquely tagged image from the caller's `cli.ImageConfig`, retains it only for the parent test, and creates a fresh container for every case. The library does not hard-code a base image: this example selects its `ubuntu:24.04` Dockerfile, while another consumer can provide a different build context and Dockerfile. At most two cases hold a slot at once; verbose output logs image and full container IDs, plus `started` and completion progress. Testcontainers cleans each case container and then the image-owning container, which removes the built image.

`Environment.CheckStdout` verifies an argv command's exact exit code and multiplexed stdout/stderr stream. `Environment.CheckFile` copies and verifies one file. `Environment.Exec` is the lower-level primitive for custom checks. The multi-command file workflow stays in this example; consumers should similarly implement multiple-file or domain-specific workflows in their own case instead of extending the library with a workflow DSL.

To test a real CLI, point `cli.ImageConfig` at a Dockerfile that builds or installs the CLI, then replace the sample argv values. Keep one parent image build, fresh case containers, and no host mounts.
