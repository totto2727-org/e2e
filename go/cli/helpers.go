package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Command describes an argv command without an implicit shell.
type Command struct {
	Args       []string
	WorkingDir string
	Env        []string
}

// Result captures each output stream separately. ExitCode is valid only when
// Run returns no error. A nonzero command exit code is not an execution error.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type Environment struct {
	ctx        context.Context
	container  runtimeContainer
	driverMu   sync.Mutex
	driverPath string
}

type runtimeContainer interface {
	Run(context.Context, Command) (Result, error)
	CopyToContainer(context.Context, []byte, string, int64) error
	CopyFileFromContainer(context.Context, string) (io.ReadCloser, error)
}

// Run executes a command within the case's context. On an execution error, the
// result retains any output already captured, but its exit code is not valid.
func (e *Environment) Run(command Command) (Result, error) {
	if err := validateCommand(command); err != nil {
		return Result{}, err
	}
	result, err := e.container.Run(e.ctx, command)
	if err != nil {
		return result, fmt.Errorf("run %q: %w", command.Args, err)
	}
	return result, nil
}

func validateCommand(command Command) error {
	if len(command.Args) == 0 || command.Args[0] == "" {
		return errors.New("command requires a nonempty executable")
	}
	for _, argument := range command.Args {
		if strings.ContainsRune(argument, '\x00') {
			return errors.New("command arguments must not contain NUL")
		}
	}
	if strings.ContainsRune(command.WorkingDir, '\x00') {
		return errors.New("command working directory must not contain NUL")
	}
	for _, entry := range command.Env {
		if strings.ContainsRune(entry, '\x00') {
			return errors.New("command environment must not contain NUL")
		}
	}
	return nil
}

func (e *Environment) CheckFile(path string, want []byte) error {
	data, err := e.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, want) {
		return fmt.Errorf("file %s: content=%q want=%q", path, data, want)
	}
	return nil
}

func (e *Environment) WriteFile(path string, content []byte) error {
	if err := e.container.CopyToContainer(e.ctx, content, path, 0o644); err != nil {
		return fmt.Errorf("copy file %s: %w", path, err)
	}
	return nil
}

func (e *Environment) ReadFile(path string) ([]byte, error) {
	reader, err := e.container.CopyFileFromContainer(e.ctx, path)
	if err != nil {
		return nil, fmt.Errorf("copy file %s: %w", path, err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}
	return data, nil
}
