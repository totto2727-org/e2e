//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/totto2727-org/e2e/go/cli"
)

func TestTerminalInteractiveScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	installDriver(t, environment)
	command := cli.Command{Args: []string{"bash", "-c", `saved=$(stty -g)
trap 'printf "size:"; stty size' WINCH
trap 'printf "signal:USR1"' USR1
stty raw -echo
printf 'stdout\000\377same|same|'
printf '\033[?1049hready:tail' >&2
dd bs=1 count=2 status=none of=/workspace/raw-input
printf 'input-done'
while :; do
  IFS= read -r -n1 key || continue
  if [ "$key" = q ]; then break; fi
done
stty "$saved"
printf '\033[?1049l' >&2
exit 7`}}
	err := environment.WithTerminal(command, cli.TerminalOptions{Rows: 24, Columns: 80}, func(terminal *cli.Terminal) error {
		for _, data := range [][]byte{[]byte("\x1b[?1049hready:"), []byte("tail")} {
			if _, err := terminal.Expect(cli.TerminalOutput, data, 3*time.Second); err != nil {
				return err
			}
		}
		for _, data := range [][]byte{[]byte("stdout\x00\xff"), []byte("same|"), []byte("same|")} {
			if _, err := terminal.Expect(cli.TerminalStdout, data, 3*time.Second); err != nil {
				return err
			}
		}
		observed, err := terminal.Observe()
		if err != nil {
			return err
		}
		if observed.Status.Kind != "running" || observed.TermiosRestored {
			return fmt.Errorf("expected running raw terminal: %+v", observed)
		}
		if _, err := terminal.SendInput([]byte{3, 4}); err != nil {
			return err
		}
		if _, err := terminal.Expect(cli.TerminalStdout, []byte("input-done"), 3*time.Second); err != nil {
			return err
		}
		if err := environment.CheckFile("/workspace/raw-input", []byte{3, 4}); err != nil {
			return err
		}
		if _, err := terminal.Resize(9, 31); err != nil {
			return err
		}
		if _, err := terminal.SendInput([]byte("x")); err != nil {
			return err
		}
		if _, err := terminal.Expect(cli.TerminalStdout, []byte("size:9 31"), 3*time.Second); err != nil {
			return err
		}
		if _, err := terminal.Signal("SIGUSR1"); err != nil {
			return err
		}
		if _, err := terminal.SendInput([]byte("x")); err != nil {
			return err
		}
		if _, err := terminal.Expect(cli.TerminalStdout, []byte("signal:USR1"), 3*time.Second); err != nil {
			return err
		}
		observed, err = terminal.Expect(cli.TerminalOutput, []byte("not-present"), 100*time.Millisecond)
		var failure *cli.TerminalError
		if !errors.As(err, &failure) || !bytes.Contains(observed.Terminal, []byte("ready:tail")) {
			return fmt.Errorf("missing expectation lost diagnostics: observation=%+v err=%v", observed, err)
		}
		if _, err := terminal.SendInput([]byte("q")); err != nil {
			return err
		}
		observed, err = terminal.Wait(3 * time.Second)
		if err != nil {
			return err
		}
		if observed.Status != (cli.TerminalStatus{Kind: "exited", ExitCode: 7}) || !observed.TermiosRestored || !bytes.HasSuffix(observed.Terminal, []byte("\x1b[?1049l")) {
			return fmt.Errorf("unexpected final terminal state: %+v", observed)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckCommand(cli.Command{Args: []string{"sh", "-c", `for file in /proc/[0-9]*/stat; do read -r pid comm state rest < "$file" 2>/dev/null || continue; if [ "$comm" = '(driver)' ] && [ "$state" != Z ]; then exit 1; fi; done`}}, cli.Result{}); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalPipeInputScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	installDriver(t, environment)
	input := []byte{0, 255, 3, 4, '\n'}
	err := environment.WithTerminal(cli.Command{
		Args:       []string{"sh", "-c", `if test -t 0; then exit 9; fi; test -t 2 || exit 10; printf '%s:%s' "$HOME" "$PWD"; printf terminal >&2; exit 0`},
		WorkingDir: "/workspace", Env: []string{"HOME=/workspace/custom"},
	}, cli.TerminalOptions{Input: input}, func(terminal *cli.Terminal) error {
		if _, err := terminal.SendInput(nil); err == nil {
			return errors.New("pipe input accepted terminal send")
		}
		observed, err := terminal.Wait(3 * time.Second)
		if err != nil {
			return err
		}
		if observed.Status != (cli.TerminalStatus{Kind: "exited", ExitCode: 0}) || string(observed.Stdout) != "/workspace/custom:/workspace" || string(observed.Terminal) != "terminal" || !bytes.Equal(observed.UnreadInput, input) {
			return fmt.Errorf("pipe observation=%+v", observed)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTerminalTinyGeometryNoOpAndSignalScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	installDriver(t, environment)
	if err := environment.WriteFile("/workspace/unchanged", []byte{0, 255}); err != nil {
		t.Fatal(err)
	}
	before, err := environment.Snapshot([]string{"/workspace/unchanged"})
	if err != nil {
		t.Fatal(err)
	}
	err = environment.WithTerminal(cli.Command{Args: []string{"sh", "-c", `if [ "$(stty size)" = '1 1' ]; then printf tiny >&2; else printf changed > /workspace/unchanged; fi; exec sleep infinity`}}, cli.TerminalOptions{Rows: 1, Columns: 1}, func(terminal *cli.Terminal) error {
		if _, err := terminal.Expect(cli.TerminalOutput, []byte("tiny"), 3*time.Second); err != nil {
			return err
		}
		if err := environment.CheckSnapshot(before); err != nil {
			return err
		}
		if _, err := terminal.Signal("SIGTERM"); err != nil {
			return err
		}
		observed, err := terminal.Wait(3 * time.Second)
		if err != nil {
			return err
		}
		if observed.Status != (cli.TerminalStatus{Kind: "signaled", Signal: 15}) {
			return fmt.Errorf("signal status=%+v", observed.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTerminalNonrootDriverScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example-nonroot:local")
	installDriver(t, environment)
	if err := environment.CheckCommand(cli.Command{Args: []string{"id", "-u"}}, cli.Result{Stdout: "65534\n"}); err != nil {
		t.Fatal(err)
	}
	before, err := environment.Snapshot([]string{"/etc/hostname"})
	if err != nil {
		t.Fatal(err)
	}
	err = environment.WithTerminal(cli.Command{Args: []string{"sh", "-c", "test -t 0 && test -t 2 && printf nonroot"}}, cli.TerminalOptions{}, func(terminal *cli.Terminal) error {
		observed, err := terminal.Wait(3 * time.Second)
		if err != nil {
			return err
		}
		if observed.Status != (cli.TerminalStatus{Kind: "exited"}) || string(observed.Stdout) != "nonroot" {
			return fmt.Errorf("nonroot terminal observation=%+v", observed)
		}
		return environment.CheckSnapshot(before)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTerminalWatchdogScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	installDriver(t, environment)
	err := environment.WithTerminal(cli.Command{Args: []string{"sh", "-c", "printf ready; exec sleep infinity"}}, cli.TerminalOptions{Timeout: 500 * time.Millisecond}, func(terminal *cli.Terminal) error {
		if _, err := terminal.Expect(cli.TerminalStdout, []byte("ready"), time.Second); err != nil {
			return err
		}
		if _, err := terminal.Expect(cli.TerminalOutput, []byte("never"), 2*time.Second); err == nil {
			return errors.New("watchdog did not end the session")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckCommand(cli.Command{Args: []string{"sh", "-c", `set -- /tmp/caseyard-pty-*; test ! -e "$1"`}}, cli.Result{}); err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckCommand(cli.Command{Args: []string{"sh", "-c", `for file in /proc/[0-9]*/stat; do read -r pid comm state rest < "$file" 2>/dev/null || continue; if [ "$comm" = '(driver)' ] && [ "$state" != Z ]; then exit 1; fi; done`}}, cli.Result{}); err != nil {
		t.Fatal(err)
	}
}
