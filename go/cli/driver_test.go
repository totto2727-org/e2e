package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInstallDriver_private_installation_is_once_only(t *testing.T) {
	container := &installDriverContainer{}
	environment := &Environment{ctx: t.Context(), container: container}
	if err := environment.InstallDriver([]byte{0, 255, 1}); err != nil {
		t.Fatal(err)
	}
	if environment.driverPath != "/tmp/caseyard-driver-owned/driver" || container.copiedPath != environment.driverPath || string(container.copiedContent) != string([]byte{0, 255, 1}) || container.copiedMode != 0o755 {
		t.Fatalf("installation path=%q content=%v mode=%o", container.copiedPath, container.copiedContent, container.copiedMode)
	}
	if err := environment.InstallDriver([]byte("replacement")); err == nil {
		t.Fatal("driver replacement accepted")
	}
	if len(container.commands) != 1 || container.commands[0].Args[0] != "mktemp" {
		t.Fatalf("commands=%+v", container.commands)
	}
}

func TestInstallDriver_rejects_empty_and_cleans_partial_installation(t *testing.T) {
	cause := errors.New("transfer rejected")
	for _, test := range []struct {
		name      string
		container *installDriverContainer
		wantCause error
	}{
		{name: "transfer", container: &installDriverContainer{fakeContainer: fakeContainer{writeErr: cause}}, wantCause: cause},
		{name: "creation", container: &installDriverContainer{createExit: 1}},
		{name: "invalid directory", container: &installDriverContainer{directory: "/not/private\n"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := &Environment{ctx: t.Context(), container: test.container}
			if err := environment.InstallDriver(nil); err == nil {
				t.Fatal("empty driver accepted")
			}
			if len(test.container.commands) != 0 {
				t.Fatal("empty driver called runtime")
			}
			err := environment.InstallDriver([]byte("driver"))
			if err == nil || (test.wantCause != nil && !errors.Is(err, test.wantCause)) {
				t.Fatalf("err=%v", err)
			}
			if environment.driverPath != "" {
				t.Fatal("failed driver installed")
			}
			if test.name == "transfer" {
				last := test.container.commands[len(test.container.commands)-1]
				if strings.Join(last.Args, " ") != "rm -rf -- /tmp/caseyard-driver-owned" {
					t.Fatalf("cleanup=%+v", last)
				}
			}
		})
	}
}

type installDriverContainer struct {
	fakeContainer
	commands   []Command
	directory  string
	createExit int
}

func (c *installDriverContainer) Run(_ context.Context, command Command) (Result, error) {
	c.commands = append(c.commands, command)
	if command.Args[0] == "mktemp" {
		directory := c.directory
		if directory == "" {
			directory = "/tmp/caseyard-driver-owned\n"
		}
		return Result{Stdout: directory, ExitCode: c.createExit}, nil
	}
	return Result{}, nil
}
