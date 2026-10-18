//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/totto2727-org/e2e/go/cli"
)

func installDriver(t *testing.T, environment *cli.Environment) {
	t.Helper()
	result, err := environment.Run(cli.Command{Args: []string{"uname", "-m"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("image architecture: %+v", result)
	}
	var architecture string
	switch strings.TrimSpace(result.Stdout) {
	case "aarch64":
		architecture = "arm64"
	case "x86_64":
		architecture = "amd64"
	default:
		t.Fatalf("unsupported image architecture %q", result.Stdout)
	}
	binary, err := os.ReadFile(fmt.Sprintf("../target/driver-linux-%s", architecture))
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.InstallDriver(binary); err != nil {
		t.Fatal(err)
	}
}
