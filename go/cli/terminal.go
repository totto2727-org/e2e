package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// TerminalOptions selects geometry, stdin mode, and an additional watchdog.
// Zero geometry and timeout use 40 rows, 120 columns, and 50 seconds.
// Nil Input connects stdin to the terminal. Non-nil Input supplies a finite pipe.
type TerminalOptions struct {
	Rows    int
	Columns int
	Input   []byte
	Timeout time.Duration
}

// TerminalChannel selects independently buffered raw output.
type TerminalChannel string

const (
	TerminalStdout TerminalChannel = "stdout"
	TerminalOutput TerminalChannel = "terminal"
)

// TerminalStatus distinguishes an application exit from signal termination.
type TerminalStatus struct {
	Kind     string
	ExitCode int
	Signal   int
}

// TerminalSettings contains the observed termios attributes, not screen state.
type TerminalSettings struct {
	InputFlags        int64
	OutputFlags       int64
	ControlFlags      int64
	LocalFlags        int64
	InputSpeed        int64
	OutputSpeed       int64
	ControlCharacters []byte
}

// TerminalObservation preserves cumulative raw output and final unread pipe input.
// A driver error can return partial output with no status or termios attributes.
type TerminalObservation struct {
	Stdout          []byte
	Terminal        []byte
	UnreadInput     []byte
	Status          TerminalStatus
	TermiosRestored bool
	InitialTermios  *TerminalSettings
	CurrentTermios  *TerminalSettings
}

// Terminal is valid only inside its WithTerminal callback. It has no Docker escape hatch.
type Terminal struct {
	gate        chan struct{}
	environment *Environment
	path        string
	driverPath  string
	closed      atomic.Bool
	cleaned     bool
	pipeInput   bool
}

// WithTerminal runs a callback with a scoped application session. It always closes
// an acquired session, including callback errors and panics. InstallDriver is required.
func (e *Environment) WithTerminal(command Command, options TerminalOptions, use func(*Terminal) error) (err error) {
	if err := validateCommand(command); err != nil {
		return err
	}
	if use == nil {
		return errors.New("terminal callback must not be nil")
	}
	if options.Rows == 0 {
		options.Rows = 40
	}
	if options.Columns == 0 {
		options.Columns = 120
	}
	if options.Timeout == 0 {
		options.Timeout = 50 * time.Second
	}
	if err := validateGeometry(options.Rows, options.Columns); err != nil {
		return err
	}
	if err := validateTerminalTimeout(options.Timeout); err != nil {
		return err
	}
	var input any
	if options.Input != nil {
		input = terminalBytes(options.Input)
	}
	request := struct {
		Args    []string `json:"argv"`
		Rows    int      `json:"rows"`
		Columns int      `json:"columns"`
		Input   any      `json:"input"`
		Timeout float64  `json:"timeout"`
	}{command.Args, options.Rows, options.Columns, input, options.Timeout.Seconds()}
	driver, err := e.installedDriver()
	if err != nil {
		return err
	}
	value, err := e.driverCall("start", request, command)
	if err != nil {
		return err
	}
	var started struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(value, &started); err != nil {
		return fmt.Errorf("decode terminal start: %w", err)
	}
	if started.Path == "" || strings.ContainsRune(started.Path, '\x00') {
		return errors.New("terminal start returned an invalid session path")
	}
	session := &Terminal{environment: e, path: started.Path, driverPath: driver, pipeInput: options.Input != nil, gate: make(chan struct{}, 1)}
	defer func() { err = errors.Join(err, session.Close()) }()
	return use(session)
}

// Observe returns current cumulative bytes without waiting for application exit.
func (t *Terminal) Observe() (TerminalObservation, error) { return t.request("observe", nil) }

// Expect matches raw bytes after the channel's consumed cursor and retains trailing output.
func (t *Terminal) Expect(channel TerminalChannel, data []byte, timeout time.Duration) (TerminalObservation, error) {
	if channel != TerminalStdout && channel != TerminalOutput {
		return TerminalObservation{}, errors.New("terminal channel must be stdout or terminal")
	}
	if len(data) == 0 {
		return TerminalObservation{}, errors.New("terminal expectation must not be empty")
	}
	if err := validateTerminalTimeout(timeout); err != nil {
		return TerminalObservation{}, err
	}
	return t.request("expect", map[string]any{"channel": channel, "data": terminalBytes(data), "timeout": timeout.Seconds()})
}

// SendInput writes literal bytes, including Ctrl-C and Ctrl-D, to terminal stdin.
func (t *Terminal) SendInput(data []byte) (TerminalObservation, error) {
	if t.pipeInput {
		return TerminalObservation{}, errors.New("cannot send terminal input when stdin is a pipe")
	}
	return t.request("send", map[string]any{"data": terminalBytes(data)})
}

// Resize changes geometry and sends SIGWINCH to the application process group.
func (t *Terminal) Resize(rows, columns int) (TerminalObservation, error) {
	if err := validateGeometry(rows, columns); err != nil {
		return TerminalObservation{}, err
	}
	return t.request("resize", map[string]any{"rows": rows, "columns": columns})
}

// Signal sends a named signal to the application process group, not terminal input.
func (t *Terminal) Signal(name string) (TerminalObservation, error) {
	switch name {
	case "SIGINT", "SIGTERM", "SIGHUP", "SIGQUIT", "SIGKILL", "SIGUSR1", "SIGUSR2":
	default:
		return TerminalObservation{}, errors.New("unsupported terminal signal")
	}
	return t.request("signal", map[string]any{"name": name})
}

// Wait drains output and returns the application exit or signal status.
func (t *Terminal) Wait(timeout time.Duration) (TerminalObservation, error) {
	if err := validateTerminalTimeout(timeout); err != nil {
		return TerminalObservation{}, err
	}
	return t.request("wait", map[string]any{"timeout": timeout.Seconds()})
}

// Close kills and reaps the owned process group and removes the session socket.
// It is idempotent and uses a fresh 30-second cleanup budget.
func (t *Terminal) Close() error {
	t.closed.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), containerCleanupTimeout)
	defer cancel()
	if err := t.acquire(ctx); err != nil {
		return err
	}
	defer t.release()
	if t.cleaned {
		return nil
	}
	cleanup := &Environment{ctx: ctx, container: t.environment.container, driverPath: t.driverPath}
	value, err := cleanup.driverCall("request", map[string]any{"path": t.path, "operation": "close"}, Command{})
	if err == nil && strings.TrimSpace(string(value)) != "null" {
		err = errors.New("terminal close response must be null")
	}
	if err == nil {
		t.cleaned = true
	}
	return err
}

func (t *Terminal) request(operation string, fields map[string]any) (TerminalObservation, error) {
	if t.environment == nil {
		return TerminalObservation{}, errors.New("uninitialized terminal session")
	}
	if err := t.acquire(t.environment.ctx); err != nil {
		return TerminalObservation{}, err
	}
	defer t.release()
	if t.closed.Load() {
		return TerminalObservation{}, errors.New("terminal session is closed")
	}
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["path"], fields["operation"] = t.path, operation
	value, err := t.environment.driverCall("request", fields, Command{})
	if err != nil {
		var driverError *TerminalError
		if errors.As(err, &driverError) {
			return driverError.Observation, err
		}
		return TerminalObservation{}, err
	}
	return decodeTerminalObservation(value, false)
}

func (t *Terminal) acquire(ctx context.Context) error {
	if t.gate == nil {
		return errors.New("uninitialized terminal session")
	}
	select {
	case t.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Terminal) release() { <-t.gate }

// TerminalError retains diagnostic observations from a failed driver operation.
type TerminalError struct {
	Message     string
	Observation TerminalObservation
}

func (e *TerminalError) Error() string { return e.Message }

func (e *Environment) driverCall(mode string, request any, command Command) (json.RawMessage, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode terminal request: %w", err)
	}
	driver, err := e.installedDriver()
	if err != nil {
		return nil, err
	}
	command.Args = []string{driver, mode, string(encoded)}
	result, err := e.Run(command)
	if err != nil {
		return nil, err
	}
	var response struct {
		OK          *bool           `json:"ok"`
		Value       json.RawMessage `json:"value"`
		Error       string          `json:"error"`
		Observation json.RawMessage `json:"observation"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return nil, fmt.Errorf("decode terminal response (exit=%d stderr=%q): %w", result.ExitCode, result.Stderr, err)
	}
	if response.OK == nil {
		return nil, errors.New("terminal response has no success flag")
	}
	if !*response.OK {
		observation := TerminalObservation{}
		if len(response.Observation) != 0 && string(response.Observation) != "null" {
			observation, err = decodeTerminalObservation(response.Observation, true)
			if err != nil {
				return nil, fmt.Errorf("decode failed terminal observation: %w", err)
			}
		}
		return nil, &TerminalError{Message: fmt.Sprintf("terminal %s: %s (exit=%d stderr=%q)", mode, response.Error, result.ExitCode, result.Stderr), Observation: observation}
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("terminal %s: helper exit=%d stderr=%q", mode, result.ExitCode, result.Stderr)
	}
	if len(response.Value) == 0 {
		return nil, errors.New("terminal response has no value")
	}
	return response.Value, nil
}

func validateGeometry(rows, columns int) error {
	if rows < 1 || rows > 65535 || columns < 1 || columns > 65535 {
		return errors.New("terminal rows and columns must be in 1..65535")
	}
	return nil
}
func validateTerminalTimeout(timeout time.Duration) error {
	if timeout <= 0 || timeout > 60*time.Second {
		return errors.New("terminal timeout must be positive and at most 60 seconds")
	}
	return nil
}

// terminalBytes uses numeric arrays, not encoding/json's base64 encoding of []byte.
func terminalBytes(data []byte) []int {
	values := make([]int, len(data))
	for index, value := range data {
		values[index] = int(value)
	}
	return values
}

type driverBytes []byte

func (b *driverBytes) UnmarshalJSON(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if values == nil {
		return errors.New("terminal bytes must be an array")
	}
	result := make([]byte, len(values))
	for index, raw := range values {
		var value int
		if string(raw) == "null" {
			return errors.New("terminal byte must not be null")
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if value < 0 || value > 255 {
			return errors.New("terminal byte outside 0..255")
		}
		result[index] = byte(value)
	}
	*b = result
	return nil
}

func decodeTerminalObservation(data json.RawMessage, partial bool) (TerminalObservation, error) {
	var wire struct {
		Stdout      *driverBytes `json:"stdout"`
		Terminal    *driverBytes `json:"terminal"`
		UnreadInput *driverBytes `json:"unreadInput"`
		Status      *struct {
			Kind   string `json:"kind"`
			Code   *int   `json:"code"`
			Signal *int   `json:"signal"`
		} `json:"status"`
		Restored *bool             `json:"termiosRestored"`
		Initial  []json.RawMessage `json:"initialTermios"`
		Current  []json.RawMessage `json:"currentTermios"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return TerminalObservation{}, fmt.Errorf("decode terminal observation: %w", err)
	}
	if wire.Stdout == nil || wire.Terminal == nil {
		return TerminalObservation{}, errors.New("terminal observation requires both output arrays")
	}
	observation := TerminalObservation{Stdout: []byte(*wire.Stdout), Terminal: []byte(*wire.Terminal)}
	if partial && wire.Status == nil {
		return observation, nil
	}
	if wire.UnreadInput == nil || wire.Status == nil || wire.Restored == nil {
		return observation, errors.New("incomplete terminal observation")
	}
	observation.UnreadInput = []byte(*wire.UnreadInput)
	observation.Status.Kind = wire.Status.Kind
	switch wire.Status.Kind {
	case "running":
		if wire.Status.Code != nil || wire.Status.Signal != nil {
			return observation, errors.New("running terminal has exit status")
		}
	case "exited":
		if wire.Status.Code == nil || *wire.Status.Code < 0 || wire.Status.Signal != nil {
			return observation, errors.New("invalid terminal exit status")
		}
		observation.Status.ExitCode = *wire.Status.Code
	case "signaled":
		if wire.Status.Signal == nil || *wire.Status.Signal <= 0 || wire.Status.Code != nil {
			return observation, errors.New("invalid terminal signal status")
		}
		observation.Status.Signal = *wire.Status.Signal
	default:
		return observation, errors.New("unknown terminal status kind")
	}
	observation.TermiosRestored = *wire.Restored
	var err error
	observation.InitialTermios, err = decodeTerminalSettings(wire.Initial)
	if err != nil {
		return observation, err
	}
	observation.CurrentTermios, err = decodeTerminalSettings(wire.Current)
	return observation, err
}

func decodeTerminalSettings(wire []json.RawMessage) (*TerminalSettings, error) {
	if len(wire) != 7 {
		return nil, errors.New("terminal settings require seven fields")
	}
	settings := &TerminalSettings{}
	for index, target := range []*int64{&settings.InputFlags, &settings.OutputFlags, &settings.ControlFlags, &settings.LocalFlags, &settings.InputSpeed, &settings.OutputSpeed} {
		if string(wire[index]) == "null" {
			return nil, errors.New("null terminal setting")
		}
		if err := json.Unmarshal(wire[index], target); err != nil {
			return nil, fmt.Errorf("decode terminal setting: %w", err)
		}
	}
	var characters driverBytes
	if err := json.Unmarshal(wire[6], &characters); err != nil {
		return nil, fmt.Errorf("decode terminal control characters: %w", err)
	}
	settings.ControlCharacters = []byte(characters)
	return settings, nil
}
