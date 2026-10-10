package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

func TestEnvironmentCheckCommand_compares_all_fields_and_applies_options(t *testing.T) {
	container := &framedContainer{stdout: "out\n", stderr: "err\n", fakeContainer: fakeContainer{exitCode: 7}}
	environment := &Environment{ctx: t.Context(), container: container}
	expectation := CommandExpectation{Command: Command{Args: []string{"example"}, WorkingDir: "/work", Env: []string{"KEY=value"}}, ExitCode: 7, Stdout: "out\n", Stderr: "err\n"}
	if err := environment.CheckCommand(expectation); err != nil {
		t.Fatal(err)
	}
	if container.config.ExecConfig.WorkingDir != "/work" || strings.Join(container.config.ExecConfig.Env, ",") != "KEY=value" {
		t.Fatalf("config=%+v", container.config.ExecConfig)
	}
	for _, field := range []string{"exit code", "stdout", "stderr"} {
		t.Run(field, func(t *testing.T) {
			mismatch := expectation
			switch field {
			case "exit code":
				mismatch.ExitCode = 0
			case "stdout":
				mismatch.Stdout = "wrong"
			case "stderr":
				mismatch.Stderr = "wrong"
			}
			if err := environment.CheckCommand(mismatch); err == nil {
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
		{name: "transport", container: &fakeContainer{execErr: failure}, wantCause: failure},
		{name: "invalid test exit", container: &fakeContainer{exitCode: 2}},
		{name: "missing test utility", container: &fakeContainer{exitCode: 127}},
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
	environment := &Environment{ctx: t.Context(), container: &fakeContainer{execErr: failure}}
	if err := environment.CheckCommand(CommandExpectation{Command: Command{Args: []string{"example"}}}); !errors.Is(err, failure) {
		t.Fatalf("err=%v want wrapped failure", err)
	}
}

type secondProbeFailureContainer struct{ fakeContainer }

func (f *secondProbeFailureContainer) Exec(_ context.Context, args []string, _ ...tcexec.ProcessOption) (int, io.Reader, error) {
	exitCode := 0
	switch args[1] {
	case "-e":
		exitCode = 1
	case "-L":
		exitCode = 127
	}
	return exitCode, strings.NewReader(""), nil
}

type localCommandContainer struct{ fakeContainer }

func (f *localCommandContainer) Exec(ctx context.Context, args []string, options ...tcexec.ProcessOption) (int, io.Reader, error) {
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	var exitError *exec.ExitError
	if err != nil && !errors.As(err, &exitError) {
		return 0, nil, err
	}
	framed := &framedContainer{stdout: stdout.String(), stderr: stderr.String(), fakeContainer: fakeContainer{exitCode: command.ProcessState.ExitCode()}}
	return framed.Exec(ctx, args, options...)
}

type readlinkFailureContainer struct {
	fakeContainer
	exitCode int
	err      error
}

func (f *readlinkFailureContainer) Exec(_ context.Context, args []string, _ ...tcexec.ProcessOption) (int, io.Reader, error) {
	if args[0] == "test" {
		return 0, strings.NewReader(""), nil
	}
	return f.exitCode, strings.NewReader(""), f.err
}
