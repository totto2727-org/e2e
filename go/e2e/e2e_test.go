//go:build e2e

package e2e

import (
	"bytes"
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
	if result, err := environment.Run(cli.Command{Args: []string{"echo", "hello from e2e"}}); err != nil {
		t.Fatal(err)
	} else if want := (cli.Result{
		ExitCode: 0,
		Stdout:   "hello from e2e\n",
		Stderr:   "",
	}); result != want {
		t.Fatalf("result=%#v want=%#v", result, want)
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
		if result, err := environment.Run(command.command); err != nil {
			t.Fatal(err)
		} else if want := command.want; result != want {
			t.Fatalf("result=%#v want=%#v", result, want)
		}
	}
	if data, err := environment.ReadFile("/workspace/result.txt"); err != nil {
		t.Fatal(err)
	} else if want := []byte("first\nsecond\n"); !bytes.Equal(data, want) {
		t.Fatalf("content=%q want=%q", data, want)
	}
	binary := []byte{0, 255, '\n'}
	if err := environment.WriteFile("/workspace/binary.bin", binary); err != nil {
		t.Fatal(err)
	}
	if data, err := environment.ReadFile("/workspace/binary.bin"); err != nil {
		t.Fatal(err)
	} else if want := binary; !bytes.Equal(data, want) {
		t.Fatalf("content=%q want=%q", data, want)
	}
	if result, err := environment.Run(cli.Command{Args: []string{"stat", "-c", "%a", "/workspace/binary.bin"}}); err != nil {
		t.Fatal(err)
	} else if want := (cli.Result{ExitCode: 0, Stdout: "644\n"}); result != want {
		t.Fatalf("result=%#v want=%#v", result, want)
	}
}

func TestExpectedFailureScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	if result, err := environment.Run(cli.Command{Args: []string{"sh", "-c", "echo expected-failure; exit 7"}}); err != nil {
		t.Fatal(err)
	} else if want := (cli.Result{
		ExitCode: 7,
		Stdout:   "expected-failure\n",
		Stderr:   "",
	}); result != want {
		t.Fatalf("result=%#v want=%#v", result, want)
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
	result, err := environment.Run(command)
	if err != nil {
		t.Fatal(err)
	}
	if result != want {
		t.Fatalf("result=%+v want=%+v", result, want)
	}
}

func TestPathObservationsScenario(t *testing.T) {
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
		{"mkfifo", "/workspace/pipe"},
	} {
		if result, err := environment.Run(cli.Command{Args: args}); err != nil {
			t.Fatal(err)
		} else if want := (cli.Result{ExitCode: 0, Stdout: "", Stderr: ""}); result != want {
			t.Fatalf("result=%#v want=%#v", result, want)
		}
	}
	for _, test := range []struct {
		path string
		want cli.PathKind
	}{
		{"/workspace/result.txt", cli.PathFile},
		{"/workspace/directory", cli.PathDirectory},
		{"/workspace/missing.txt", cli.PathMissing},
		{"/workspace/link", cli.PathSymlink},
		{"/workspace/dangling", cli.PathSymlink},
		{"/workspace/directory-link", cli.PathSymlink},
		{"/workspace/pipe", cli.PathOther},
	} {
		got, err := environment.PathKind(test.path)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("path %q kind=%q want=%q", test.path, got, test.want)
		}
	}
	for _, test := range []struct{ path, want string }{
		{"/workspace/link", "result.txt"},
		{"/workspace/dangling", "missing.txt"},
		{"/workspace/directory-link", "directory"},
	} {
		got, err := environment.ReadSymlink(test.path)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("path %q target=%q want=%q", test.path, got, test.want)
		}
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
		if kind, err := environment.PathKind("/workspace/isolation-marker.txt"); err != nil {
			t.Fatal(err)
		} else if kind != cli.PathMissing {
			t.Fatalf("kind=%q want=%q", kind, cli.PathMissing)
		}
		if err := environment.WriteFile("/workspace/isolation-marker.txt", []byte("first case\n")); err != nil {
			t.Fatal(err)
		}
		if data, err := environment.ReadFile("/workspace/isolation-marker.txt"); err != nil {
			t.Fatal(err)
		} else if want := []byte("first case\n"); !bytes.Equal(data, want) {
			t.Fatalf("content=%q want=%q", data, want)
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
		if kind, err := environment.PathKind("/workspace/isolation-marker.txt"); err != nil {
			t.Fatal(err)
		} else if kind != cli.PathMissing {
			t.Fatalf("kind=%q want=%q", kind, cli.PathMissing)
		}
	})
}
