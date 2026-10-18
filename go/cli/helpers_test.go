package cli

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
)

func TestEnvironmentRun_preserves_result_and_command(t *testing.T) {
	want := Result{ExitCode: 7, Stdout: "result\n", Stderr: "diagnostic\n"}
	container := &fakeContainer{result: want}
	environment := &Environment{ctx: t.Context(), container: container}
	command := Command{
		Args:       []string{"example", "arg with spaces", "", "literal ; $(not-a-command)"},
		WorkingDir: "/workspace/../project",
		Env:        []string{"HOME=/workspace/home", "XDG_CONFIG_HOME=/workspace/config", "BARE"},
	}

	result, err := environment.Run(command)

	if err != nil {
		t.Fatal(err)
	}
	if result != want {
		t.Fatalf("result=%+v want=%+v", result, want)
	}
	if !reflect.DeepEqual(container.command, command) {
		t.Fatalf("command=%+v want=%+v", container.command, command)
	}
	if container.runCalls != 1 {
		t.Fatalf("runtime calls=%d want=1", container.runCalls)
	}
}

func TestEnvironmentRun_rejects_invalid_command_without_runtime_call(t *testing.T) {
	for _, test := range []struct {
		name    string
		command Command
	}{
		{name: "nil args"},
		{name: "empty args", command: Command{Args: []string{}}},
		{name: "empty executable", command: Command{Args: []string{"", "argument"}}},
		{name: "NUL executable", command: Command{Args: []string{"ex\x00ample"}}},
		{name: "NUL argument", command: Command{Args: []string{"example", "arg\x00ument"}}},
		{name: "NUL working directory", command: Command{Args: []string{"example"}, WorkingDir: "/work\x00space"}},
		{name: "NUL environment", command: Command{Args: []string{"example"}, Env: []string{"VALID=value", "KEY=val\x00ue"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			container := &fakeContainer{}
			environment := &Environment{ctx: t.Context(), container: container}
			if _, err := environment.Run(test.command); err == nil {
				t.Fatal("expected invalid command error")
			}
			if container.runCalls != 0 {
				t.Fatalf("invalid command invoked runtime %d times", container.runCalls)
			}
		})
	}
}

func TestEnvironmentRun_preserves_partial_result_and_error(t *testing.T) {
	failure := errors.New("transport failed")
	want := Result{Stdout: "partial out\n", Stderr: "partial err\n"}
	environment := &Environment{ctx: t.Context(), container: &fakeContainer{result: want, runErr: failure}}

	result, err := environment.Run(Command{Args: []string{"example"}})

	if !errors.Is(err, failure) {
		t.Fatalf("err=%v want wrapped failure", err)
	}
	if result != want {
		t.Fatalf("result=%+v want=%+v", result, want)
	}
}

func TestEnvironmentReadFile_reports_close_failure(t *testing.T) {
	container := &fakeContainer{file: closeErrorReader{Reader: strings.NewReader("content\n")}}
	environment := &Environment{ctx: t.Context(), container: container}
	if _, err := environment.ReadFile("/workspace/result.txt"); err == nil {
		t.Fatal("expected close failure")
	}
}

func TestEnvironmentWriteFile_copies_content_with_standard_mode(t *testing.T) {
	container := &fakeContainer{}
	environment := &Environment{ctx: t.Context(), container: container}
	path := "/workspace/../input.json"
	content := []byte("{\"name\":\"example\"}\n")

	if err := environment.WriteFile(path, content); err != nil {
		t.Fatal(err)
	}
	if container.copiedPath != path {
		t.Fatalf("path=%q want=%q", container.copiedPath, path)
	}
	if got, want := string(container.copiedContent), string(content); got != want {
		t.Fatalf("content=%q want=%q", got, want)
	}
	if container.copiedMode != 0o644 {
		t.Fatalf("mode=%#o", container.copiedMode)
	}
}

func TestEnvironmentReadFile_returns_copied_content(t *testing.T) {
	container := &fakeContainer{file: io.NopCloser(strings.NewReader("content\n"))}
	environment := &Environment{ctx: t.Context(), container: container}

	content, err := environment.ReadFile("/workspace/result.txt")

	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "content\n"; got != want {
		t.Fatalf("content=%q want=%q", got, want)
	}
}

func writeFrame(writer io.Writer, stream stdcopy.StdType, content string) error {
	var header [8]byte
	header[0] = byte(stream)
	binary.BigEndian.PutUint32(header[4:], uint32(len(content)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	if content == "" {
		return nil
	}
	_, err := io.WriteString(writer, content)
	return err
}

type fakeContainer struct {
	result        Result
	runErr        error
	runCalls      int
	file          io.ReadCloser
	copyErr       error
	command       Command
	copiedContent []byte
	copiedPath    string
	copiedMode    int64
	readPath      string
	writeErr      error
}

func (f *fakeContainer) Run(_ context.Context, command Command) (Result, error) {
	f.command = command
	f.runCalls++
	return f.result, f.runErr
}

func (f *fakeContainer) CopyToContainer(_ context.Context, content []byte, path string, mode int64) error {
	f.copiedContent = content
	f.copiedPath = path
	f.copiedMode = mode
	return f.writeErr
}

func (f *fakeContainer) CopyFileFromContainer(_ context.Context, path string) (io.ReadCloser, error) {
	f.readPath = path
	return f.file, f.copyErr
}

type closeErrorReader struct {
	io.Reader
}

func (closeErrorReader) Close() error {
	return errors.New("close")
}
