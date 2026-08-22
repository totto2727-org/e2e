package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

type Result struct {
	ExitCode int
	Stdout   string
}

type Command struct {
	Args       []string
	WorkingDir string
	Env        []string
}

type StdoutExpectation struct {
	Command  []string
	ExitCode int
	Stdout   string
}

type File struct {
	Path    string
	Content []byte
}

type FileExpectation = File

type Environment struct {
	ctx       context.Context
	container runtimeContainer
}

type runtimeContainer interface {
	GetContainerID() string
	Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error)
	CopyToContainer(context.Context, []byte, string, int64) error
	CopyFileFromContainer(context.Context, string) (io.ReadCloser, error)
}

func (e *Environment) Exec(command []string) (Result, error) {
	return e.Run(Command{Args: command})
}

func (e *Environment) Run(command Command) (Result, error) {
	options := []tcexec.ProcessOption{tcexec.Multiplexed()}
	if command.WorkingDir != "" {
		options = append(options, tcexec.WithWorkingDir(command.WorkingDir))
	}
	if len(command.Env) > 0 {
		options = append(options, tcexec.WithEnv(command.Env))
	}
	exitCode, output, err := e.container.Exec(e.ctx, command.Args, options...)
	if err != nil {
		return Result{ExitCode: exitCode}, fmt.Errorf("exec %q: %w", command.Args, err)
	}
	data, err := io.ReadAll(output)
	if err != nil {
		return Result{ExitCode: exitCode}, fmt.Errorf("read output for %q: %w", command.Args, err)
	}
	return Result{ExitCode: exitCode, Stdout: string(data)}, nil
}

func (e *Environment) CheckStdout(expectation StdoutExpectation) error {
	result, err := e.Exec(expectation.Command)
	if err != nil {
		return err
	}
	if result.ExitCode != expectation.ExitCode || result.Stdout != expectation.Stdout {
		return fmt.Errorf(
			"command %q: exit_code=%d stdout=%q want_exit_code=%d want_stdout=%q",
			expectation.Command,
			result.ExitCode,
			result.Stdout,
			expectation.ExitCode,
			expectation.Stdout,
		)
	}
	return nil
}

func (e *Environment) CheckFile(expectation FileExpectation) error {
	data, err := e.ReadFile(expectation.Path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, expectation.Content) {
		return fmt.Errorf("file %s: content=%q want=%q", expectation.Path, data, expectation.Content)
	}
	return nil
}

func (e *Environment) WriteFile(file File) error {
	if err := e.container.CopyToContainer(e.ctx, file.Content, file.Path, 0o644); err != nil {
		return fmt.Errorf("copy file %s: %w", file.Path, err)
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
