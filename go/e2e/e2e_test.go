//go:build e2e

package e2e

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/totto2727-org/e2e/go/cli"
)

func TestStdoutScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	if err := environment.CheckCommand(cli.Command{Args: []string{"echo", "hello from e2e"}}, cli.Result{
		ExitCode: 0,
		Stdout:   "hello from e2e\n",
		Stderr:   "",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFileOutputScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	commands := []struct {
		command cli.Command
		want    cli.Result
	}{
		{command: cli.Command{Args: []string{"sh", "-c", "echo first > /workspace/result.txt"}}, want: cli.Result{ExitCode: 0, Stdout: "", Stderr: ""}},
		{command: cli.Command{Args: []string{"sh", "-c", "echo second >> /workspace/result.txt"}}, want: cli.Result{ExitCode: 0, Stdout: "", Stderr: ""}},
		{command: cli.Command{Args: []string{"cat", "/workspace/result.txt"}}, want: cli.Result{ExitCode: 0, Stdout: "first\nsecond\n", Stderr: ""}},
	}
	for _, command := range commands {
		if err := environment.CheckCommand(command.command, command.want); err != nil {
			t.Fatal(err)
		}
	}
	if err := environment.CheckFile("/workspace/result.txt", []byte("first\nsecond\n")); err != nil {
		t.Fatal(err)
	}
	binary := []byte{0, 255, '\n'}
	if err := environment.WriteFile("/workspace/binary.bin", binary); err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckFile("/workspace/binary.bin", binary); err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckCommand(cli.Command{Args: []string{"stat", "-c", "%a", "/workspace/binary.bin"}}, cli.Result{ExitCode: 0, Stdout: "644\n"}); err != nil {
		t.Fatal(err)
	}
}

func TestExpectedFailureScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	if err := environment.CheckCommand(cli.Command{Args: []string{"sh", "-c", "echo expected-failure; exit 7"}}, cli.Result{
		ExitCode: 7,
		Stdout:   "expected-failure\n",
		Stderr:   "",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSeparatedOutputScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	command := cli.Command{
		Args:       []string{"sh", "-c", `printf '%s:%s\n' "$HOME" "$PWD"; printf 'diagnostic\n' >&2; exit 7`},
		WorkingDir: "/workspace",
		Env:        []string{"HOME=/workspace/synthetic-home"},
	}
	want := cli.Result{
		ExitCode: 7,
		Stdout:   "/workspace/synthetic-home:/workspace\n",
		Stderr:   "diagnostic\n",
	}
	if err := environment.CheckCommand(command, want); err != nil {
		t.Fatal(err)
	}
	result, err := environment.Run(command)
	if err != nil {
		t.Fatal(err)
	}
	if result != want {
		t.Fatalf("result=%+v want=%+v", result, want)
	}
}

func TestPathAssertionsScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	if err := environment.WriteFile("/workspace/result.txt", []byte("fixture\n")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"mkdir", "/workspace/directory"},
		{"ln", "-s", "result.txt", "/workspace/link"},
		{"ln", "-s", "missing.txt", "/workspace/dangling"},
		{"ln", "-s", "directory", "/workspace/directory-link"},
	} {
		if err := environment.CheckCommand(cli.Command{Args: args}, cli.Result{ExitCode: 0, Stdout: "", Stderr: ""}); err != nil {
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
	if err := environment.CheckDirectory("/workspace/directory-link"); err == nil {
		t.Fatal("symbolic link was treated as a physical directory")
	}
	if err := environment.CheckSymlink("/workspace/link", "/workspace/result.txt"); err == nil {
		t.Fatal("resolved target was accepted instead of the literal target")
	}
}

func TestDeadlineScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	started := time.Now()
	_, err := environment.Run(cli.Command{Args: []string{"sleep", "infinity"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sleep error=%v want deadline exceeded", err)
	}
	// Allow scheduling overhead around the existing one-minute case deadline.
	if elapsed := time.Since(started); elapsed > 70*time.Second {
		t.Fatalf("run exceeded its case deadline: elapsed=%s", elapsed)
	}
}

func TestIsolationScenario(t *testing.T) {
	t.Parallel()
	var firstHostname string
	if !t.Run("first", func(t *testing.T) {
		environment := cli.NewEnvironment(t, "e2e-example:local")
		result, err := environment.Run(cli.Command{Args: []string{"cat", "/etc/hostname"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.ExitCode != 0 || result.Stderr != "" || strings.TrimSpace(result.Stdout) == "" {
			t.Fatalf("hostname result=%+v want exit 0, nonempty hostname, and empty stderr", result)
		}
		firstHostname = result.Stdout
		if err := environment.CheckMissing("/workspace/isolation-marker.txt"); err != nil {
			t.Fatal(err)
		}
		if err := environment.WriteFile("/workspace/isolation-marker.txt", []byte("first case\n")); err != nil {
			t.Fatal(err)
		}
		if err := environment.CheckFile("/workspace/isolation-marker.txt", []byte("first case\n")); err != nil {
			t.Fatal(err)
		}
	}) {
		return
	}
	t.Run("second", func(t *testing.T) {
		environment := cli.NewEnvironment(t, "e2e-example:local")
		result, err := environment.Run(cli.Command{Args: []string{"cat", "/etc/hostname"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.ExitCode != 0 || result.Stderr != "" || strings.TrimSpace(result.Stdout) == "" {
			t.Fatalf("hostname result=%+v want exit 0, nonempty hostname, and empty stderr", result)
		}
		if result.Stdout == firstHostname {
			t.Fatalf("case containers reported the same hostname: %q", result.Stdout)
		}
		if err := environment.CheckMissing("/workspace/isolation-marker.txt"); err != nil {
			t.Fatal(err)
		}
	})
}
