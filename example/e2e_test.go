package example

import (
	"testing"

	"github.com/totto2727-org/e2e/cli"
)

func TestCLI(t *testing.T) {
	cli.Run(t, cli.ImageConfig{Context: ".", Dockerfile: "Dockerfile"}, []cli.Case{
		{Name: "stdout", Run: stdoutScenario},
		{Name: "file_output", Run: fileOutputScenario},
		{Name: "expected_failure", Run: expectedFailureScenario},
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
