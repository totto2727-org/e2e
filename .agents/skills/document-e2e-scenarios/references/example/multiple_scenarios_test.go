package example

import (
	"testing"

	"github.com/totto2727-org/e2e/go/cli"
)

func TestCLI(t *testing.T) {
	t.Run("prints greeting", func(t *testing.T) {
		t.Parallel()
		printsGreetingScenario(t, cli.NewEnvironment(t, "example-cli:local"))
	})
	t.Run("returns expected failure", func(t *testing.T) {
		t.Parallel()
		returnsExpectedFailureScenario(t, cli.NewEnvironment(t, "example-cli:local"))
	})
}

func printsGreetingScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	if err := environment.CheckCommand(cli.Command{Args: []string{"echo", "hello from e2e"}}, cli.Result{
		ExitCode: 0,
		Stdout:   "hello from e2e\n",
		Stderr:   "",
	}); err != nil {
		t.Fatal(err)
	}
}

func returnsExpectedFailureScenario(t *testing.T, environment *cli.Environment) {
	t.Helper()
	if err := environment.CheckCommand(cli.Command{Args: []string{"false"}}, cli.Result{
		ExitCode: 1,
		Stdout:   "",
		Stderr:   "",
	}); err != nil {
		t.Fatal(err)
	}
}
