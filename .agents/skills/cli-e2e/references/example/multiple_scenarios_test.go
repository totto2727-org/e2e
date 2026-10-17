package example

import (
	"testing"

	"github.com/totto2727-org/e2e/go/cli"
)

func TestPrintsGreetingScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "example-cli:local")
	if err := environment.CheckCommand(cli.Command{Args: []string{"echo", "hello from e2e"}}, cli.Result{
		ExitCode: 0,
		Stdout:   "hello from e2e\n",
		Stderr:   "",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReturnsExpectedFailureScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "example-cli:local")
	if err := environment.CheckCommand(cli.Command{Args: []string{"false"}}, cli.Result{
		ExitCode: 1,
		Stdout:   "",
		Stderr:   "",
	}); err != nil {
		t.Fatal(err)
	}
}
