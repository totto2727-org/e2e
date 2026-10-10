package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
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

func TestEnvironmentRunSeparated_preserves_streams_and_options(t *testing.T) {
	container := &framedContainer{stdout: "out\n", stderr: "err\n", fakeContainer: fakeContainer{exitCode: 9}}
	environment := &Environment{ctx: t.Context(), container: container}
	result, err := environment.RunSeparated(Command{Args: []string{"example", "arg with spaces"}, WorkingDir: "/workspace", Env: []string{"KEY=value"}})
	if err != nil {
		t.Fatal(err)
	}
	if result != (CommandResult{ExitCode: 9, Stdout: "out\n", Stderr: "err\n"}) {
		t.Fatalf("result=%+v", result)
	}
	if container.config.ExecConfig.WorkingDir != "/workspace" || strings.Join(container.config.ExecConfig.Env, ",") != "KEY=value" {
		t.Fatalf("config=%+v", container.config.ExecConfig)
	}
}

func TestEnvironmentLegacyOutput_concatenates_stdout_then_stderr(t *testing.T) {
	container := &framedContainer{stdout: "out\n", stderr: "err\n"}
	environment := &Environment{ctx: t.Context(), container: container}
	for _, run := range []func() (Result, error){
		func() (Result, error) { return environment.Run(Command{Args: []string{"example"}}) },
		func() (Result, error) { return environment.Exec([]string{"example"}) },
	} {
		result, err := run()
		if err != nil {
			t.Fatal(err)
		}
		if result.Stdout != "out\nerr\n" {
			t.Fatalf("result=%+v", result)
		}
	}
	if err := environment.CheckStdout(StdoutExpectation{Command: []string{"example"}, Stdout: "out\nerr\n"}); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentRunSeparated_reports_errors(t *testing.T) {
	failure := errors.New("transport failed")
	for _, test := range []struct {
		name      string
		container runtimeContainer
	}{
		{name: "exec", container: &fakeContainer{execErr: failure}},
		{name: "demultiplex", container: &framedContainer{reader: failingReader{err: failure}}},
		{name: "output read", container: &framedContainer{outputReader: failingReader{err: failure}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := &Environment{ctx: t.Context(), container: test.container}
			if _, err := environment.RunSeparated(Command{Args: []string{"example"}}); !errors.Is(err, failure) {
				t.Fatalf("err=%v want wrapped failure", err)
			}
		})
	}
}

func TestEnvironmentRunSeparated_accepts_plain_stdout_reader(t *testing.T) {
	environment := &Environment{ctx: t.Context(), container: &fakeContainer{output: "plain"}}
	result, err := environment.RunSeparated(Command{Args: []string{"example"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "plain" || result.Stderr != "" {
		t.Fatalf("result=%+v", result)
	}
}

type framedContainer struct {
	fakeContainer
	stdout       string
	stderr       string
	reader       io.Reader
	outputReader io.Reader
	config       *tcexec.ProcessOptions
}

func (f *framedContainer) Exec(ctx context.Context, command []string, options ...tcexec.ProcessOption) (int, io.Reader, error) {
	f.command = command
	f.options = options
	f.config = tcexec.NewProcessOptions(command)
	for _, option := range options {
		option.Apply(f.config)
	}
	var stream bytes.Buffer
	// Write stderr first to verify the legacy API groups output by stream, not arrival order.
	if err := writeFrame(&stream, stdcopy.Stderr, f.stderr); err != nil {
		return 0, nil, err
	}
	if err := writeFrame(&stream, stdcopy.Stdout, f.stdout); err != nil {
		return 0, nil, err
	}
	f.config.Reader = &stream
	if f.reader != nil {
		f.config.Reader = f.reader
	}
	for _, option := range options {
		option.Apply(f.config)
	}
	if f.outputReader != nil {
		f.config.Reader = f.outputReader
	}
	return f.exitCode, f.config.Reader, f.execErr
}

func writeFrame(writer io.Writer, stream stdcopy.StdType, content string) error {
	var header [8]byte
	header[0] = byte(stream)
	binary.BigEndian.PutUint32(header[4:], uint32(len(content)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := io.WriteString(writer, content)
	return err
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

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
