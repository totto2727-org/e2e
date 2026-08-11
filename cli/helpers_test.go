package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

func TestEnvironmentExec_returns_exit_code_and_output(t *testing.T) {
	container := &fakeContainer{exitCode: 7, output: "failed\n"}
	environment := &Environment{ctx: t.Context(), container: container}

	result, err := environment.Exec([]string{"example"})

	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || result.Stdout != "failed\n" {
		t.Fatalf("result=%+v", result)
	}
}

func TestEnvironmentCheckStdout_accepts_exact_result(t *testing.T) {
	container := &fakeContainer{output: "hello\n"}
	environment := &Environment{ctx: t.Context(), container: container}

	err := environment.CheckStdout(StdoutExpectation{
		Command:  []string{"echo", "hello"},
		ExitCode: 0,
		Stdout:   "hello\n",
	})

	if err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentCheckStdout_rejects_mismatch(t *testing.T) {
	container := &fakeContainer{output: "actual\n"}
	environment := &Environment{ctx: t.Context(), container: container}

	err := environment.CheckStdout(StdoutExpectation{
		Command:  []string{"echo", "actual"},
		ExitCode: 0,
		Stdout:   "expected\n",
	})

	if err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestEnvironmentCheckFile_accepts_exact_content(t *testing.T) {
	container := &fakeContainer{file: io.NopCloser(strings.NewReader("content\n"))}
	environment := &Environment{ctx: t.Context(), container: container}

	err := environment.CheckFile(FileExpectation{
		Path:    "/workspace/result.txt",
		Content: []byte("content\n"),
	})

	if err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentCheckFile_rejects_mismatch(t *testing.T) {
	container := &fakeContainer{file: io.NopCloser(strings.NewReader("actual\n"))}
	environment := &Environment{ctx: t.Context(), container: container}

	err := environment.CheckFile(FileExpectation{
		Path:    "/workspace/result.txt",
		Content: []byte("expected\n"),
	})

	if err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestEnvironmentCheckFile_reports_close_failure(t *testing.T) {
	container := &fakeContainer{file: closeErrorReader{Reader: strings.NewReader("content\n")}}
	environment := &Environment{ctx: t.Context(), container: container}

	err := environment.CheckFile(FileExpectation{
		Path:    "/workspace/result.txt",
		Content: []byte("content\n"),
	})

	if err == nil {
		t.Fatal("expected close failure")
	}
}

type fakeContainer struct {
	exitCode int
	output   string
	execErr  error
	file     io.ReadCloser
	copyErr  error
}

func (f *fakeContainer) GetContainerID() string {
	return "container-id"
}

func (f *fakeContainer) Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error) {
	return f.exitCode, strings.NewReader(f.output), f.execErr
}

func (f *fakeContainer) CopyFileFromContainer(context.Context, string) (io.ReadCloser, error) {
	return f.file, f.copyErr
}

type closeErrorReader struct {
	io.Reader
}

func (closeErrorReader) Close() error {
	return errors.New("close")
}
