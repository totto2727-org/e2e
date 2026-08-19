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

func TestEnvironmentRun_applies_working_directory_and_environment(t *testing.T) {
	// Given
	container := &fakeContainer{output: "ok\n"}
	environment := &Environment{ctx: t.Context(), container: container}

	// When
	result, err := environment.Run(Command{
		Args:       []string{"c-plugin", "sync"},
		WorkingDir: "/workspace/project",
		Env:        []string{"HOME=/workspace/home", "XDG_CONFIG_HOME=/workspace/config"},
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "ok\n" {
		t.Fatalf("stdout=%q", result.Stdout)
	}
	options := tcexec.NewProcessOptions(container.command)
	for _, option := range container.options {
		option.Apply(options)
	}
	if options.ExecConfig.WorkingDir != "/workspace/project" {
		t.Fatalf("working_dir=%q", options.ExecConfig.WorkingDir)
	}
	if got, want := strings.Join(options.ExecConfig.Env, ","), "HOME=/workspace/home,XDG_CONFIG_HOME=/workspace/config"; got != want {
		t.Fatalf("env=%q want=%q", got, want)
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

func TestEnvironmentWriteFile_copies_content_with_standard_mode(t *testing.T) {
	// Given
	container := &fakeContainer{}
	environment := &Environment{ctx: t.Context(), container: container}
	file := File{Path: "/workspace/input.json", Content: []byte("{\"name\":\"example\"}\n")}

	// When
	err := environment.WriteFile(file)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if container.copiedPath != file.Path {
		t.Fatalf("path=%q want=%q", container.copiedPath, file.Path)
	}
	if got, want := string(container.copiedContent), string(file.Content); got != want {
		t.Fatalf("content=%q want=%q", got, want)
	}
	if container.copiedMode != 0o644 {
		t.Fatalf("mode=%#o", container.copiedMode)
	}
}

func TestEnvironmentReadFile_returns_copied_content(t *testing.T) {
	// Given
	container := &fakeContainer{file: io.NopCloser(strings.NewReader("content\n"))}
	environment := &Environment{ctx: t.Context(), container: container}

	// When
	content, err := environment.ReadFile("/workspace/result.txt")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "content\n"; got != want {
		t.Fatalf("content=%q want=%q", got, want)
	}
}

type fakeContainer struct {
	exitCode      int
	output        string
	execErr       error
	file          io.ReadCloser
	copyErr       error
	command       []string
	options       []tcexec.ProcessOption
	copiedContent []byte
	copiedPath    string
	copiedMode    int64
	writeErr      error
}

func (f *fakeContainer) GetContainerID() string {
	return "container-id"
}

func (f *fakeContainer) Exec(_ context.Context, command []string, options ...tcexec.ProcessOption) (int, io.Reader, error) {
	f.command = command
	f.options = options
	return f.exitCode, strings.NewReader(f.output), f.execErr
}

func (f *fakeContainer) CopyToContainer(_ context.Context, content []byte, path string, mode int64) error {
	f.copiedContent = content
	f.copiedPath = path
	f.copiedMode = mode
	return f.writeErr
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
