# Terminal scenarios

Source: [terminal_test.go](./terminal_test.go)

This document is for contributors who maintain Go Docker acceptance tests.
Use the [repository validation workflow](../../AGENTS.md#validation) to build the fixture images and static driver artifacts.
The [shared fixture helper](./driver_test.go) installs the executable for the actual container architecture.
The application image has neither Python nor Go.

## `TestTerminalInteractiveScenario`

Run a Bash application with terminal stdin and stderr, separate stdout, and a 24-by-80 geometry.
Match raw ANSI bytes and binary stdout, including NUL and `0xff`.
Repeated expectations verify consumed cursors and retained trailing bytes.
Send literal Ctrl-C and Ctrl-D bytes in raw mode and check their exact bytes in a file through the ordinary environment API.
Resize to 9-by-31 and observe the application's `SIGWINCH` response.
Send `SIGUSR1` independently of terminal input.
The shell's builtin read is given a neutral byte after each signal so trap execution does not depend on shell scheduling.
A bounded failed expectation must retain its captured diagnostic observation.
Exit with application status 7, restore termios, and emit the literal screen-restoration sequence.
The scope must close its terminal after the callback.

## `TestTerminalPipeInputScenario`

Supply finite binary pipe input to an application that does not consume stdin.
Verify pipe stdin, terminal stderr, inherited working directory and environment, exact separate output, and all unread bytes after exit.
Terminal input operations must fail for pipe stdin.

## `TestTerminalTinyGeometryNoOpAndSignalScenario`

Use a 1-by-1 terminal to exercise an application no-op path.
Check an existing binary file against a snapshot inside the terminal callback.
Deliver `SIGTERM` and verify signal termination rather than an application exit code.

## `TestTerminalNonrootDriverScenario`

Use `e2e-example-nonroot:local` and verify UID 65534.
Install the caller-supplied driver without a chmod operation, capture a readable file, and run a terminal application.
Check exact stdout, exit zero, and unchanged file state after application exit.

## `TestTerminalWatchdogScenario`

Start an indefinite application with a 500-millisecond watchdog.
Wait for its explicit readiness output, then require a never-matching expectation to fail when the watchdog ends the session.
The scope close must succeed after watchdog cleanup.
Require the owned socket directory to be absent and no live driver process to remain.
The normal-close scenario also checks for no live driver process.
For the non-reaping PID 1 boundary, see [shared terminal lifecycle](../../README.md#terminal-sessions).
