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

type StdoutExpectation struct {
	Command  []string
	ExitCode int
	Stdout   string
}

type FileExpectation struct {
	Path    string
	Content []byte
}

type Environment struct {
	ctx       context.Context
	container runtimeContainer
}

type runtimeContainer interface {
	GetContainerID() string
	Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error)
	CopyFileFromContainer(context.Context, string) (io.ReadCloser, error)
}

func (e *Environment) Exec(command []string) (Result, error) {
	exitCode, output, err := e.container.Exec(e.ctx, command, tcexec.Multiplexed())
	if err != nil {
		return Result{ExitCode: exitCode}, fmt.Errorf("exec %q: %w", command, err)
	}
	data, err := io.ReadAll(output)
	if err != nil {
		return Result{ExitCode: exitCode}, fmt.Errorf("read output for %q: %w", command, err)
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
	reader, err := e.container.CopyFileFromContainer(e.ctx, expectation.Path)
	if err != nil {
		return fmt.Errorf("copy file %s: %w", expectation.Path, err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read file %s: %w", expectation.Path, err)
	}
	if !bytes.Equal(data, expectation.Content) {
		return fmt.Errorf("file %s: content=%q want=%q", expectation.Path, data, expectation.Content)
	}
	return nil
}
