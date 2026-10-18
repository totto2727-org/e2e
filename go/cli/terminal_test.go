package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const terminalObservationJSON = `{"stdout":[0,255],"terminal":[27,91,109],"unreadInput":[3,4],"status":{"kind":"exited","code":7},"termiosRestored":true,"initialTermios":[1,2,3,4,5,6,[3,4]],"currentTermios":[1,2,3,4,5,6,[3,4]]}`

func TestTerminalClose_rejects_nonnull_acknowledgements(t *testing.T) {
	for _, value := range []string{`{}`, `7`, `[]`} {
		container := &fakeContainer{result: Result{Stdout: `{"ok":true,"value":` + value + `}`}}
		terminal := &Terminal{environment: &Environment{ctx: t.Context(), container: container}, driverPath: "/driver", path: "/session", gate: make(chan struct{}, 1)}
		if err := terminal.Close(); err == nil {
			t.Fatalf("invalid close value accepted: %s", value)
		}
		if terminal.cleaned || !terminal.closed.Load() {
			t.Fatal("failed close did not invalidate handle or marked cleanup successful")
		}
		if _, err := terminal.Observe(); err == nil {
			t.Fatal("failed close left handle usable")
		}
		if container.runCalls != 1 {
			t.Fatal("invalidated handle called runtime")
		}
	}
}

func TestTerminal_gate_wait_respects_context(t *testing.T) {
	terminal := &Terminal{gate: make(chan struct{}, 1)}
	terminal.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err := terminal.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("gate error=%v", err)
	}
}

func TestTerminal_forwards_typed_requests_and_preserves_observation_bytes(t *testing.T) {
	container := &terminalProtocolContainer{}
	environment := &Environment{ctx: t.Context(), container: container, driverPath: "/private/driver"}
	var escaped *Terminal
	command := Command{Args: []string{"application", "arg with spaces", ""}, WorkingDir: "/work", Env: []string{"HOME=/custom"}}
	err := environment.WithTerminal(command, TerminalOptions{Rows: 9, Columns: 31}, func(terminal *Terminal) error {
		escaped = terminal
		operations := []func() (TerminalObservation, error){
			terminal.Observe,
			func() (TerminalObservation, error) {
				return terminal.Expect(TerminalOutput, []byte{0, 255}, time.Second)
			},
			func() (TerminalObservation, error) { return terminal.SendInput([]byte{3, 4}) },
			func() (TerminalObservation, error) { return terminal.Resize(12, 40) },
			func() (TerminalObservation, error) { return terminal.Signal("SIGINT") },
			func() (TerminalObservation, error) { return terminal.Wait(time.Second) },
		}
		for _, operation := range operations {
			observed, err := operation()
			if err != nil {
				return err
			}
			if !bytes.Equal(observed.Stdout, []byte{0, 255}) || !bytes.Equal(observed.Terminal, []byte{27, 91, 109}) || !bytes.Equal(observed.UnreadInput, []byte{3, 4}) || observed.Status != (TerminalStatus{Kind: "exited", ExitCode: 7}) || !observed.TermiosRestored || !reflect.DeepEqual(observed.InitialTermios, observed.CurrentTermios) {
				t.Fatalf("observation=%+v", observed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if container.closeCalls != 1 {
		t.Fatalf("close calls=%d", container.closeCalls)
	}
	if _, err := escaped.Observe(); err == nil {
		t.Fatal("escaped terminal remained usable")
	}
	if err := escaped.Close(); err != nil {
		t.Fatal(err)
	}
	if container.startCommand.WorkingDir != command.WorkingDir || !reflect.DeepEqual(container.startCommand.Env, command.Env) {
		t.Fatalf("start options=%+v", container.startCommand)
	}
	var start struct {
		Args    []string        `json:"argv"`
		Rows    int             `json:"rows"`
		Columns int             `json:"columns"`
		Input   json.RawMessage `json:"input"`
		Timeout float64         `json:"timeout"`
	}
	if err := json.Unmarshal([]byte(container.startCommand.Args[2]), &start); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(start.Args, command.Args) || start.Rows != 9 || start.Columns != 31 || string(start.Input) != "null" || start.Timeout != 50 {
		t.Fatalf("start=%+v", start)
	}
	if got, want := container.operations, []string{"observe", "expect", "send", "resize", "signal", "wait", "close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("operations=%q want=%q", got, want)
	}
	if !strings.Contains(container.requests[1], `"data":[0,255]`) || !strings.Contains(container.requests[2], `"data":[3,4]`) {
		t.Fatalf("byte requests=%q", container.requests)
	}
}

func TestTerminal_scope_cleanup_preserves_callback_failure_and_fresh_context(t *testing.T) {
	cause := errors.New("callback failed")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	container := &terminalProtocolContainer{}
	environment := &Environment{ctx: ctx, container: container, driverPath: "/driver"}
	err := environment.WithTerminal(Command{Args: []string{"app"}}, TerminalOptions{}, func(*Terminal) error { cancel(); return cause })
	if !errors.Is(err, cause) || container.closeCalls != 1 || container.closeCanceled {
		t.Fatalf("err=%v closes=%d canceled=%t", err, container.closeCalls, container.closeCanceled)
	}
}

func TestTerminal_scope_cleanup_on_panic(t *testing.T) {
	cause := errors.New("callback panic")
	container := &terminalProtocolContainer{}
	environment := &Environment{ctx: t.Context(), container: container, driverPath: "/driver"}
	defer func() {
		if recovered := recover(); recovered != cause || container.closeCalls != 1 {
			t.Errorf("panic=%v closes=%d", recovered, container.closeCalls)
		}
	}()
	if err := environment.WithTerminal(Command{Args: []string{"app"}}, TerminalOptions{}, func(*Terminal) error { panic(cause) }); err != nil {
		t.Fatal(err)
	}
}

func TestTerminal_pipe_input_distinguishes_empty_pipe_from_terminal_stdin(t *testing.T) {
	for _, input := range [][]byte{{}, {0, 255}} {
		container := &terminalProtocolContainer{}
		environment := &Environment{ctx: t.Context(), container: container, driverPath: "/driver"}
		err := environment.WithTerminal(Command{Args: []string{"app"}}, TerminalOptions{Input: input}, func(terminal *Terminal) error {
			if _, err := terminal.SendInput(nil); err == nil {
				t.Fatal("pipe accepted terminal input")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(terminalBytes(input))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(container.startCommand.Args[2], `"input":`+string(want)) {
			t.Fatalf("start input=%s", container.startCommand.Args[2])
		}
	}
}

func TestTerminal_errors_preserve_partial_observations_and_cleanup_failure(t *testing.T) {
	container := &terminalProtocolContainer{operationError: true, closeError: errors.New("close transport")}
	environment := &Environment{ctx: t.Context(), container: container, driverPath: "/driver"}
	var escaped *Terminal
	err := environment.WithTerminal(Command{Args: []string{"app"}}, TerminalOptions{}, func(terminal *Terminal) error {
		escaped = terminal
		observed, err := terminal.Expect(TerminalOutput, []byte("missing"), time.Second)
		var failure *TerminalError
		if !errors.As(err, &failure) || !bytes.Equal(observed.Stdout, []byte{0, 255}) || !strings.Contains(err.Error(), "awaited missing") {
			t.Fatalf("observed=%+v err=%v", observed, err)
		}
		return err
	})
	var failure *TerminalError
	if !errors.As(err, &failure) || !errors.Is(err, container.closeError) {
		t.Fatalf("err=%v want expectation and cleanup failure", err)
	}
	calls := len(container.operations)
	if _, err := escaped.Observe(); err == nil {
		t.Fatal("failed scope cleanup left handle usable")
	}
	if len(container.operations) != calls {
		t.Fatal("escaped handle invoked runtime after scope exit")
	}
	container.closeError = nil
	if err := escaped.Close(); err != nil {
		t.Fatal(err)
	}
	if container.closeCalls != 2 {
		t.Fatalf("cleanup retry calls=%d want=2", container.closeCalls)
	}
}

func TestTerminal_validation_and_protocol_failures(t *testing.T) {
	for _, options := range []TerminalOptions{{Rows: -1}, {Columns: 65536}, {Timeout: -1}, {Timeout: 61 * time.Second}} {
		container := &fakeContainer{}
		environment := &Environment{ctx: t.Context(), container: container, driverPath: "/driver"}
		if err := environment.WithTerminal(Command{Args: []string{"app"}}, options, func(*Terminal) error { return nil }); err == nil {
			t.Fatal("invalid options accepted")
		}
		if container.runCalls != 0 {
			t.Fatal("invalid options called runtime")
		}
	}
	for _, payload := range []string{"null", `"base64"`, "[null]", "[256]", "[-1]", "[1.5]", "[true]"} {
		var data driverBytes
		if err := json.Unmarshal([]byte(payload), &data); err == nil {
			t.Fatalf("invalid bytes accepted: %s", payload)
		}
	}
	for _, response := range []string{"{", "{}", `{"ok":true}`, `{"ok":true,"value":{}}`, `{"ok":false,"error":"failure","observation":{"stdout":[256],"terminal":[]}}`} {
		environment := &Environment{ctx: t.Context(), container: &fakeContainer{result: Result{Stdout: response}}, driverPath: "/driver"}
		if err := environment.WithTerminal(Command{Args: []string{"app"}}, TerminalOptions{}, func(*Terminal) error { return nil }); err == nil {
			t.Fatalf("invalid response accepted: %s", response)
		}
	}
	for _, observation := range []string{`{}`, `{"stdout":[],"terminal":[]}`, strings.Replace(terminalObservationJSON, `"kind":"exited","code":7`, `"kind":"other"`, 1), strings.Replace(terminalObservationJSON, `"initialTermios":[1,2,3,4,5,6,[3,4]]`, `"initialTermios":[]`, 1)} {
		if _, err := decodeTerminalObservation(json.RawMessage(observation), false); err == nil {
			t.Fatalf("invalid observation accepted: %s", observation)
		}
	}
	container := &fakeContainer{}
	terminal := &Terminal{environment: &Environment{ctx: t.Context(), container: container, driverPath: "/driver"}}
	for _, operation := range []func() error{
		func() error { _, err := terminal.Expect("unknown", []byte("x"), time.Second); return err },
		func() error { _, err := terminal.Expect(TerminalOutput, nil, time.Second); return err },
		func() error { _, err := terminal.Expect(TerminalOutput, []byte("x"), 0); return err },
		func() error { _, err := terminal.Resize(0, 80); return err },
		func() error { _, err := terminal.Signal("SIGWINCH"); return err },
		func() error { _, err := terminal.Wait(-1); return err },
	} {
		if err := operation(); err == nil {
			t.Fatal("invalid operation accepted")
		}
	}
	if container.runCalls != 0 {
		t.Fatal("invalid operations called runtime")
	}
	if err := (&Environment{}).WithTerminal(Command{Args: []string{"app"}}, TerminalOptions{}, func(*Terminal) error { return nil }); err == nil {
		t.Fatal("terminal without installed driver accepted")
	}
}

type terminalProtocolContainer struct {
	fakeContainer
	startCommand   Command
	operations     []string
	requests       []string
	closeCalls     int
	closeCanceled  bool
	closeError     error
	operationError bool
}

func (c *terminalProtocolContainer) Run(ctx context.Context, command Command) (Result, error) {
	if command.Args[1] == "start" {
		c.startCommand = command
		return Result{Stdout: `{"ok":true,"value":{"path":"/tmp/caseyard-pty-owned/socket"}}`}, nil
	}
	var request struct {
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal([]byte(command.Args[2]), &request); err != nil {
		return Result{}, err
	}
	c.operations = append(c.operations, request.Operation)
	c.requests = append(c.requests, command.Args[2])
	if request.Operation == "close" {
		c.closeCalls++
		c.closeCanceled = ctx.Err() != nil
		return Result{Stdout: `{"ok":true,"value":null}`}, c.closeError
	}
	if c.operationError {
		return Result{ExitCode: 1, Stdout: `{"ok":false,"error":"awaited missing","observation":` + terminalObservationJSON + `}`}, nil
	}
	return Result{Stdout: `{"ok":true,"value":` + terminalObservationJSON + `}`}, nil
}
