package example

import (
	"testing"

	"github.com/totto2727-org/e2e/go/cli"
)

func TestPrintsGreetingScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "example-cli:local")
	result, err := environment.Run(cli.Command{Args: []string{"echo", "hello from e2e"}})
	if err != nil {
		t.Fatal(err)
	}
	want := cli.Result{
		ExitCode: 0,
		Stdout:   "hello from e2e\n",
		Stderr:   "",
	}
	if result != want {
		t.Fatalf("result=%#v want=%#v", result, want)
	}
}

func TestReturnsExpectedFailureScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "example-cli:local")
	result, err := environment.Run(cli.Command{Args: []string{"false"}})
	if err != nil {
		t.Fatal(err)
	}
	want := cli.Result{
		ExitCode: 1,
		Stdout:   "",
		Stderr:   "",
	}
	if result != want {
		t.Fatalf("result=%#v want=%#v", result, want)
	}
}
