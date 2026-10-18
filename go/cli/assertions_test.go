package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnvironmentPathObservations_reject_invalid_paths_before_execution(t *testing.T) {
	for _, path := range []string{"", "bad\x00path"} {
		container := &fakeContainer{}
		environment := &Environment{ctx: t.Context(), container: container}
		if _, err := environment.PathKind(path); err == nil {
			t.Fatalf("PathKind accepted %q", path)
		}
		if _, err := environment.ReadSymlink(path); err == nil {
			t.Fatalf("ReadSymlink accepted %q", path)
		}
		if container.runCalls != 0 {
			t.Fatalf("invalid path executed %d commands", container.runCalls)
		}
	}
}

func TestEnvironmentPathKind_physical_types_and_literal_targets(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file ; $(not-a-command)")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{"file link": filepath.Base(file), "directory link": "directory", "dangling": "../absent target\n\xff"}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	environment := &Environment{ctx: t.Context(), container: &localCommandContainer{}}
	for _, test := range []struct {
		path string
		want PathKind
	}{
		{file, PathFile}, {directory, PathDirectory}, {filepath.Join(root, "absent"), PathMissing},
		{filepath.Join(root, "file link"), PathSymlink}, {filepath.Join(root, "directory link"), PathSymlink}, {filepath.Join(root, "dangling"), PathSymlink},
	} {
		t.Run(filepath.Base(test.path), func(t *testing.T) {
			got, err := environment.PathKind(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("kind=%q want=%q", got, test.want)
			}
		})
	}
	for name, want := range links {
		t.Run(name+" target", func(t *testing.T) {
			got, err := environment.ReadSymlink(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("target=%q want=%q", got, want)
			}
		})
	}
	if _, err := environment.ReadSymlink(file); err == nil {
		t.Fatal("regular file accepted as symlink")
	}
}

func TestEnvironmentPathKind_rejects_infrastructure_failures(t *testing.T) {
	failure := errors.New("daemon unavailable")
	for _, test := range []struct {
		name      string
		container runtimeContainer
		wantCause error
	}{
		{"transport", &fakeContainer{runErr: failure}, failure},
		{"invalid test exit", &fakeContainer{result: Result{ExitCode: 2}}, nil},
		{"missing test utility", &fakeContainer{result: Result{ExitCode: 127}}, nil},
		{"later probe", &pathProbeContainer{failFlag: "-d"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := &Environment{ctx: t.Context(), container: test.container}
			_, err := environment.PathKind("path")
			if err == nil || (test.wantCause != nil && !errors.Is(err, test.wantCause)) {
				t.Fatalf("err=%v wantCause=%v", err, test.wantCause)
			}
		})
	}
}

func TestEnvironmentPathKind_other(t *testing.T) {
	environment := &Environment{ctx: t.Context(), container: &pathProbeContainer{}}
	got, err := environment.PathKind("path")
	if err != nil {
		t.Fatal(err)
	}
	if got != PathOther {
		t.Fatalf("kind=%q want=%q", got, PathOther)
	}
}

func TestEnvironmentReadSymlink_reports_failures(t *testing.T) {
	failure := errors.New("readlink transport")
	for _, test := range []struct {
		name      string
		container *fakeContainer
		wantCause error
	}{
		{"exit", &fakeContainer{result: Result{ExitCode: 1}}, nil},
		{"transport", &fakeContainer{runErr: failure}, failure},
		{"malformed output", &fakeContainer{result: Result{Stdout: "unterminated"}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := &Environment{ctx: t.Context(), container: test.container}
			_, err := environment.ReadSymlink("path")
			if err == nil || (test.wantCause != nil && !errors.Is(err, test.wantCause)) {
				t.Fatalf("err=%v wantCause=%v", err, test.wantCause)
			}
		})
	}
}

type pathProbeContainer struct {
	fakeContainer
	failFlag string
}

func (f *pathProbeContainer) Run(_ context.Context, command Command) (Result, error) {
	if command.Args[1] == f.failFlag {
		return Result{ExitCode: 127}, nil
	}
	if command.Args[1] == "-e" {
		return Result{}, nil
	}
	return Result{ExitCode: 1}, nil
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
