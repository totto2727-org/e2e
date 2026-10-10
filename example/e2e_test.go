package example

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/totto2727-org/e2e/cli"
)

func TestCLI(t *testing.T) {
	cli.Run(t, "e2e-example:local", []cli.Case{
		{Name: "stdout", Run: stdoutScenario},
		{Name: "file_output", Run: fileOutputScenario},
		{Name: "expected_failure", Run: expectedFailureScenario},
		{Name: "separated_output", Run: separatedOutputScenario},
		{Name: "path_assertions", Run: pathAssertionsScenario},
		{Name: "deadline", Run: deadlineScenario},
	})
}

func stdoutScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	if err := environment.CheckStdout(cli.StdoutExpectation{
		Command:  []string{"echo", "hello from e2e"},
		ExitCode: 0,
		Stdout:   "hello from e2e\n",
	}); err != nil {
		t.Fatal(err)
	}
}

func fileOutputScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	commands := []cli.StdoutExpectation{
		{Command: []string{"sh", "-c", "echo first > /workspace/result.txt"}},
		{Command: []string{"sh", "-c", "echo second >> /workspace/result.txt"}},
		{Command: []string{"cat", "/workspace/result.txt"}, Stdout: "first\nsecond\n"},
	}
	for _, command := range commands {
		if err := environment.CheckStdout(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := environment.CheckFile(cli.FileExpectation{
		Path:    "/workspace/result.txt",
		Content: []byte("first\nsecond\n"),
	}); err != nil {
		t.Fatal(err)
	}
}

func expectedFailureScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	if err := environment.CheckStdout(cli.StdoutExpectation{
		Command:  []string{"sh", "-c", "echo expected-failure; exit 7"},
		ExitCode: 7,
		Stdout:   "expected-failure\n",
	}); err != nil {
		t.Fatal(err)
	}
}

func separatedOutputScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	command := cli.Command{
		Args:       []string{"sh", "-c", `printf '%s:%s\n' "$HOME" "$PWD"; printf 'diagnostic\n' >&2; exit 7`},
		WorkingDir: "/workspace",
		Env:        []string{"HOME=/workspace/synthetic-home"},
	}
	if err := environment.CheckCommand(cli.CommandExpectation{
		Command: command, ExitCode: 7,
		Stdout: "/workspace/synthetic-home:/workspace\n", Stderr: "diagnostic\n",
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := environment.Run(command)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ExitCode != 7 || legacy.Stdout != "/workspace/synthetic-home:/workspace\ndiagnostic\n" {
		t.Fatalf("legacy result=%+v", legacy)
	}
}

func pathAssertionsScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	if err := environment.WriteFile(cli.File{Path: "/workspace/result.txt", Content: []byte("fixture\n")}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"mkdir", "/workspace/directory"},
		{"ln", "-s", "result.txt", "/workspace/link"},
		{"ln", "-s", "missing.txt", "/workspace/dangling"},
	} {
		if err := environment.CheckCommand(cli.CommandExpectation{Command: cli.Command{Args: args}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range []error{
		environment.CheckRegularFile("/workspace/result.txt"),
		environment.CheckDirectory("/workspace/directory"),
		environment.CheckMissing("/workspace/missing.txt"),
		environment.CheckSymlink("/workspace/link", "result.txt"),
		environment.CheckSymlink("/workspace/dangling", "missing.txt"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := environment.CheckMissing("/workspace/dangling"); err == nil {
		t.Fatal("dangling symbolic link was treated as missing")
	}
	if err := environment.CheckRegularFile("/workspace/link"); err == nil {
		t.Fatal("symbolic link was treated as a physical regular file")
	}
	if err := environment.CheckSymlink("/workspace/link", "/workspace/result.txt"); err == nil {
		t.Fatal("resolved target was accepted instead of the literal target")
	}
}

func deadlineScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	started := time.Now()
	_, err := environment.Exec([]string{"sleep", "infinity"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sleep error=%v want deadline exceeded", err)
	}
	// Allow scheduling overhead around the existing one-minute case deadline.
	if elapsed := time.Since(started); elapsed > 70*time.Second {
		t.Fatalf("exec exceeded its case deadline: elapsed=%s", elapsed)
	}
}
