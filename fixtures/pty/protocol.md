# Internal terminal driver protocol

The implementation is the static Linux executable built from `go/cmd/caseyard-driver` and `go/internal/driver`.
The caller supplies the matching amd64 or arm64 binary as bytes once per environment.
The SDK installs it in an unpredictable private directory using the existing file transfer and command operations.
Generated binaries are not committed or embedded into the language packages.
The target image needs neither Python nor Go.
Existing APIs retain their prerequisites.

All requests execute `INSTALLED_DRIVER MODE JSON` through the existing environment command operation.
Responses on stdout are one JSON object: `{"ok":true,"value":VALUE}` or `{"ok":false,"error":"diagnostic","observation":OBSERVATION_OR_NULL}`.
A helper infrastructure failure exits nonzero.
Child application failure is represented in an observation, never as a helper failure.
Wrappers must validate JSON and all byte arrays and propagate helper failures as errors, including captured observations where possible.

## Snapshot

MODE is `snapshot`, JSON is `{"paths":["/path",...]}`.
Returns a sorted unique array of `{"path":"/path","kind":"missing|file|directory|symlink","data":[bytes],"target":[bytes]}`.
`data` is nonempty only for file contents, `target` only for literal symlink target bytes.
Both fields are always present, including empty arrays.
Only a missing physical path means missing.
In particular, an inaccessible or non-directory parent is an error, not an absent leaf.
Other read or lstat failures and unsupported special files fail the complete snapshot.
Acquisition is not atomic across paths or against concurrent writes.

## Start

MODE is `start`, JSON is `{"argv":["executable",...],"rows":40,"columns":120,"input":null,"timeout":50}`.
Use the original target Command's working directory and environment entries on the helper invocation so the application inherits them.
`input:null` connects stdin to the PTY; an array of bytes supplies finite pipe input plus EOF instead, with stderr still PTY.
Rows and columns must be integers in 1..65535.
Timeout is finite seconds, positive and at most 60.
It is an additional terminal watchdog, not an extension of the environment budget.
The default public terminal timeout should be 50 seconds.
The helper forks a session server and returns `{"path":"/tmp/caseyard-pty-UNPREDICTABLE/socket"}` only after the server has spawned the child and is listening.
Socket directories have mode 0700.
The application is a session leader with a controlling terminal and foreground process group.
Stdout is separately piped, stdin/stderr are terminal unless pipe input was selected.
Environment cleanup remains the final process boundary, including a lost start response.

## Session requests

MODE is `request`, JSON is `{"path":"...","operation":"...",...}`.

- `observe`: return cumulative raw bytes and process/termios observations without waiting for exit.
- `expect`: `channel:"stdout|terminal"`, `data:[nonempty bytes]`, `timeout:positive seconds` bounded by terminal watchdog. Match raw bytes across reads. Each channel has a consumed cursor. Advance to match end, retaining trailing buffered output. On premature exit or timeout, fail with channel, awaited bytes and full observation.
- `send`: `data:[bytes]`. Write all bytes to the PTY with bounded backpressure. Ctrl-C and Ctrl-D are literal input, not signal/transport-close aliases. Reject when pipe stdin selected.
- `resize`: `rows`, `columns`. Update geometry and send SIGWINCH to application process group.
- `signal`: `name:"SIGINT|SIGTERM|SIGHUP|SIGQUIT|SIGKILL|SIGUSR1|SIGUSR2"`. Send to application process group.
- `wait`: optional `timeout:positive seconds`. Drain output and return final observation after child exit.
- `close`: kill any surviving application process group, reap child, close descriptors, remove owned socket directory, stop server. Return null. Repeated close after removal is treated as success by helper. All other operations after close fail.

Every successful operation except `close` returns an observation:

```json
{"stdout":[bytes],"terminal":[bytes],"unreadInput":[bytes],"status":{"kind":"running"},"termiosRestored":true,"initialTermios":[...],"currentTermios":[...]}
```

Status variants are `{"kind":"running"}`, `{"kind":"exited","code":0}`, or `{"kind":"signaled","signal":2}`.
Termios values are JSON-safe arrays of integers and nested integer arrays.
PENDIN is excluded as kernel bookkeeping, window size is reported by resize behavior rather than compared as termios.
Raw output, including ANSI bytes, remains unchanged.
`unreadInput` is populated after process exit for finite pipe input.
A restored termios flag while running is only an observation, not proof of application restoration.
Screen and cursor restoration assertions remain caller-owned using raw terminal bytes.
There is no cross-channel ordering guarantee.
The session callback can run ordinary environment file checks between terminal operations for tiny-terminal/no-op checks.
Callers may make bounded negative observations using their normal test framework timers and `observe`, without treating sleeps as readiness.

## Scope cleanup

Each language must close a successfully acquired terminal when the session scope exits, including callback failure.
On whole environment cancellation, container teardown is the final boundary if the ordinary command API can no longer make a close request.
Do not promise cancellation guarantees stronger than the environment backend supports.
The server watchdog always kills the owned process group even when the client disappears.
When the image's PID 1 does not reap adopted processes, an exited session server can remain as a zombie until container teardown.
Scoped cleanup stops the application and server, but does not promise that an arbitrary image reaps every orphan process.
A public terminal handle must not let consumers issue arbitrary driver JSON or inspect a Docker client.
