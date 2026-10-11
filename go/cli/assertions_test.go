package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnvironmentCheckCommand_compares_all_fields_and_applies_options(t *testing.T) {
	container := &fakeContainer{result: Result{ExitCode: 7, Stdout: "out\n", Stderr: "err\n"}}
	environment := &Environment{ctx: t.Context(), container: container}
	command := Command{Args: []string{"example"}, WorkingDir: "/work", Env: []string{"KEY=value"}}
	want := container.result
	if err := environment.CheckCommand(command, want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(container.command, command) {
		t.Fatalf("command=%+v want=%+v", container.command, command)
	}
	for _, field := range []string{"exit code", "stdout", "stderr"} {
		t.Run(field, func(t *testing.T) {
			mismatch := want
			switch field {
			case "exit code":
				mismatch.ExitCode = 0
			case "stdout":
				mismatch.Stdout = "wrong"
			case "stderr":
				mismatch.Stderr = "wrong"
			}
			if err := environment.CheckCommand(command, mismatch); err == nil {
				t.Fatal("expected mismatch")
			}
		})
	}
}

func TestEnvironmentPathAssertions_physical_types_and_literal_targets(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file ; $(not-a-command)")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	fileLink := filepath.Join(root, "file link")
	if err := os.Symlink(filepath.Base(file), fileLink); err != nil {
		t.Fatal(err)
	}
	directoryLink := filepath.Join(root, "directory link")
	if err := os.Symlink("directory", directoryLink); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "dangling")
	literal := "../absent target\n"
	if err := os.Symlink(literal, dangling); err != nil {
		t.Fatal(err)
	}
	environment := &Environment{ctx: t.Context(), container: &localCommandContainer{}}
	for _, test := range []struct {
		name      string
		check     func() error
		wantError bool
	}{
		{name: "missing", check: func() error { return environment.CheckMissing(filepath.Join(root, "absent")) }},
		{name: "existing", check: func() error { return environment.CheckMissing(file) }, wantError: true},
		{name: "dangling is not missing", check: func() error { return environment.CheckMissing(dangling) }, wantError: true},
		{name: "regular file", check: func() error { return environment.CheckRegularFile(file) }},
		{name: "directory is not file", check: func() error { return environment.CheckRegularFile(directory) }, wantError: true},
		{name: "file link is not physical file", check: func() error { return environment.CheckRegularFile(fileLink) }, wantError: true},
		{name: "directory", check: func() error { return environment.CheckDirectory(directory) }},
		{name: "file is not directory", check: func() error { return environment.CheckDirectory(file) }, wantError: true},
		{name: "directory link is not physical directory", check: func() error { return environment.CheckDirectory(directoryLink) }, wantError: true},
		{name: "literal relative target", check: func() error { return environment.CheckSymlink(fileLink, filepath.Base(file)) }},
		{name: "literal dangling newline target", check: func() error { return environment.CheckSymlink(dangling, literal) }},
		{name: "resolved target rejected", check: func() error { return environment.CheckSymlink(fileLink, file) }, wantError: true},
		{name: "file is not symlink", check: func() error { return environment.CheckSymlink(file, "anything") }, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.check(); (err != nil) != test.wantError {
				t.Fatalf("err=%v wantError=%t", err, test.wantError)
			}
		})
	}
}

func TestEnvironmentAssertions_reject_infrastructure_failures(t *testing.T) {
	failure := errors.New("daemon unavailable")
	for _, test := range []struct {
		name      string
		container runtimeContainer
		wantCause error
	}{
		{name: "transport", container: &fakeContainer{runErr: failure}, wantCause: failure},
		{name: "invalid test exit", container: &fakeContainer{result: Result{ExitCode: 2}}},
		{name: "missing test utility", container: &fakeContainer{result: Result{ExitCode: 127}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := &Environment{ctx: t.Context(), container: test.container}
			for _, check := range []func() error{
				func() error { return environment.CheckMissing("path") },
				func() error { return environment.CheckRegularFile("path") },
				func() error { return environment.CheckDirectory("path") },
				func() error { return environment.CheckSymlink("path", "target") },
			} {
				err := check()
				if err == nil || (test.wantCause != nil && !errors.Is(err, test.wantCause)) {
					t.Fatalf("err=%v wantCause=%v", err, test.wantCause)
				}
			}
		})
	}
}

func TestEnvironmentCheckSymlink_reports_readlink_failure(t *testing.T) {
	failure := errors.New("readlink transport")
	for _, test := range []struct {
		name      string
		container *readlinkFailureContainer
		wantCause error
	}{
		{name: "exit", container: &readlinkFailureContainer{exitCode: 1}},
		{name: "transport", container: &readlinkFailureContainer{err: failure}, wantCause: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := &Environment{ctx: t.Context(), container: test.container}
			err := environment.CheckSymlink("path", "target")
			if err == nil || (test.wantCause != nil && !errors.Is(err, test.wantCause)) {
				t.Fatalf("err=%v wantCause=%v", err, test.wantCause)
			}
		})
	}
}

func TestEnvironmentAssertions_reject_second_probe_failure(t *testing.T) {
	environment := &Environment{ctx: t.Context(), container: &secondProbeFailureContainer{}}
	for _, check := range []func() error{
		func() error { return environment.CheckMissing("path") },
		func() error { return environment.CheckRegularFile("path") },
		func() error { return environment.CheckDirectory("path") },
	} {
		if err := check(); err == nil || !strings.Contains(err.Error(), "unexpected exit_code=127") {
			t.Fatalf("err=%v want unexpected exit code", err)
		}
	}
}

func TestEnvironmentCheckCommand_preserves_infrastructure_error(t *testing.T) {
	failure := errors.New("exec failed")
	environment := &Environment{ctx: t.Context(), container: &fakeContainer{runErr: failure}}
	if err := environment.CheckCommand(Command{Args: []string{"example"}}, Result{}); !errors.Is(err, failure) {
		t.Fatalf("err=%v want wrapped failure", err)
	}
}

type secondProbeFailureContainer struct{ fakeContainer }

func (f *secondProbeFailureContainer) Run(_ context.Context, command Command) (Result, error) {
	exitCode := 0
	switch command.Args[1] {
	case "-e":
		exitCode = 1
	case "-L":
		exitCode = 127
	}
	return Result{ExitCode: exitCode}, nil
}

type localCommandContainer struct{ fakeContainer }

func (f *localCommandContainer) Run(ctx context.Context, request Command) (Result, error) {
	command := exec.CommandContext(ctx, request.Args[0], request.Args[1:]...)
	command.Dir = request.WorkingDir
	if len(request.Env) > 0 {
		command.Env = append(os.Environ(), request.Env...)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitError *exec.ExitError
	if err != nil && !errors.As(err, &exitError) {
		return result, err
	}
	result.ExitCode = command.ProcessState.ExitCode()
	return result, nil
}

type readlinkFailureContainer struct {
	fakeContainer
	exitCode int
	err      error
}

func (f *readlinkFailureContainer) Run(_ context.Context, command Command) (Result, error) {
	if command.Args[0] == "test" {
		return Result{}, nil
	}
	return Result{ExitCode: f.exitCode}, f.err
}
