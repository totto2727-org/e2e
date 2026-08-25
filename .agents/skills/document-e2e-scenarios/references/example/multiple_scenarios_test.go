package example

import (
	"testing"

	"github.com/totto2727-org/e2e/cli"
)

func TestCLI(t *testing.T) {
	cli.Run(t, "example-cli:local", []cli.Case{
		{Name: "prints greeting", Run: printsGreetingScenario},
		{Name: "returns expected failure", Run: returnsExpectedFailureScenario},
	})
}

func printsGreetingScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	if err := environment.CheckStdout(cli.StdoutExpectation{
		Command:  []string{"echo", "hello from e2e"},
		ExitCode: 0,
		Stdout:   "hello from e2e\n",
	}); err != nil {
		t.Fatal(err)
	}
}

func returnsExpectedFailureScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	if err := environment.CheckStdout(cli.StdoutExpectation{
		Command:  []string{"false"},
		ExitCode: 1,
		Stdout:   "",
	}); err != nil {
		t.Fatal(err)
	}
}
