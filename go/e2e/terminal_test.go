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
printf '\033[?1049hpro' >&2
printf fragment-ready
dd bs=1 count=1 status=none of=/dev/null
printf 'mpt>ready:tail' >&2
printf 'stdout\000\377same|same|'
dd bs=1 count=11 status=none of=/workspace/raw-input
printf 'input-done'
while :; do
  IFS= read -r -n1 key || continue
  if [ "$key" = q ]; then break; fi
done
stty "$saved"
printf '\033[?1049l' >&2
exit 7`}}
	err := environment.WithTerminal(command, cli.TerminalOptions{Rows: 24, Columns: 80}, func(terminal *cli.Terminal) error {
		if _, err := terminal.Expect(cli.TerminalStdout, []byte("fragment-ready"), 3*time.Second); err != nil {
			return err
		}
		fragment, err := terminal.Expect(cli.TerminalOutput, []byte("\x1b[?1049hprompt>"), 100*time.Millisecond)
		if err == nil || !bytes.HasSuffix(fragment.Terminal, []byte("\x1b[?1049hpro")) {
			return fmt.Errorf("incomplete prompt unexpectedly matched or fragment missing: terminal=%q err=%v", fragment.Terminal, err)
		}
		if _, err := terminal.SendInput([]byte("x")); err != nil {
			return err
		}
		if _, err := terminal.Expect(cli.TerminalOutput, []byte("\x1b[?1049hprompt>"), 3*time.Second); err != nil {
			return err
		}
		for _, data := range [][]byte{[]byte("ready:"), []byte("tail")} {
			if _, err := terminal.Expect(cli.TerminalOutput, data, 3*time.Second); err != nil {
				return err
			}
		}
		for _, data := range [][]byte{[]byte("stdout\x00\xff"), []byte("same|"), []byte("same|")} {
			if _, err := terminal.Expect(cli.TerminalStdout, data, 3*time.Second); err != nil {
				return err
			}
		}
		if _, err := terminal.Expect(cli.TerminalStdout, []byte("same|"), 100*time.Millisecond); err == nil {
			return errors.New("consumed stdout match was reused after both occurrences")
		}
		observed, err := terminal.Observe()
		if err != nil {
			return err
		}
		if observed.Status.Kind != "running" || observed.TermiosRestored {
			return fmt.Errorf("expected running raw terminal: %+v", observed)
		}
		keys := []byte{'\x1b', '[', 'A', '\x1b', '[', 'B', ' ', '\r', '\x1b', 4, 3}
		if _, err := terminal.SendInput(keys); err != nil {
			return err
		}
		if _, err := terminal.Expect(cli.TerminalStdout, []byte("input-done"), 3*time.Second); err != nil {
			return err
		}
		if err := environment.CheckFile("/workspace/raw-input", keys); err != nil {
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

func TestTerminalArgumentsAndEnvironmentScenario(t *testing.T) {
	t.Parallel()
	for _, terminalType := range []string{"xterm-256color", "caseyard-unsupported"} {
		t.Run(terminalType, func(t *testing.T) {
			t.Parallel()
			environment := cli.NewEnvironment(t, "e2e-example:local")
			installDriver(t, environment)
			before, err := environment.Snapshot([]string{"/workspace/argv-not-executed"})
			if err != nil {
				t.Fatal(err)
			}
			command := cli.Command{
				Args:       []string{"sh", "-c", `printf 'argc:%s\n' "$#"; for argument do printf 'arg:<%s>\n' "$argument"; done; printf 'cwd:%s\nhome:%s\nterm:%s\n' "$PWD" "$HOME" "$TERM"; if [ "$TERM" = caseyard-unsupported ]; then printf 'unsupported TERM' >&2; exit 2; fi`, "caseyard-argv", "argument with spaces", "", "$(touch /workspace/argv-not-executed)"},
				WorkingDir: "/workspace", Env: []string{"HOME=/workspace/terminal-home", "TERM=" + terminalType},
			}
			err = environment.WithTerminal(command, cli.TerminalOptions{}, func(terminal *cli.Terminal) error {
				observed, err := terminal.Wait(3 * time.Second)
				if err != nil {
					return err
				}
				wantOutput := "argc:3\narg:<argument with spaces>\narg:<>\narg:<$(touch /workspace/argv-not-executed)>\ncwd:/workspace\nhome:/workspace/terminal-home\nterm:" + terminalType + "\n"
				wantExit, wantTerminal := 0, ""
				if terminalType == "caseyard-unsupported" {
					wantExit, wantTerminal = 2, "unsupported TERM"
				}
				if string(observed.Stdout) != wantOutput || string(observed.Terminal) != wantTerminal || observed.Status != (cli.TerminalStatus{Kind: "exited", ExitCode: wantExit}) || !observed.TermiosRestored {
					return fmt.Errorf("argv/environment observation=%+v want stdout=%q terminal=%q exit=%d", observed, wantOutput, wantTerminal, wantExit)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := environment.CheckSnapshot(before); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTerminalConsumedPipeEOFScenario(t *testing.T) {
	t.Parallel()
	for _, input := range [][]byte{{}, {0, 255, 3, 4, '\n'}} {
		t.Run(fmt.Sprintf("bytes-%d", len(input)), func(t *testing.T) {
			t.Parallel()
			environment := cli.NewEnvironment(t, "e2e-example:local")
			installDriver(t, environment)
			err := environment.WithTerminal(cli.Command{Args: []string{"sh", "-c", "test -t 0 && exit 9; cat; printf terminal >&2"}}, cli.TerminalOptions{Input: input}, func(terminal *cli.Terminal) error {
				observed, err := terminal.Wait(3 * time.Second)
				if err != nil {
					return err
				}
				if !bytes.Equal(observed.Stdout, input) || len(observed.UnreadInput) != 0 || string(observed.Terminal) != "terminal" || observed.Status != (cli.TerminalStatus{Kind: "exited"}) {
					return fmt.Errorf("finite pipe EOF observation=%+v input=%v", observed, input)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTerminalSIGINTCancellationSnapshotScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	installDriver(t, environment)
	if err := environment.WriteFile("/workspace/guard", []byte{0, 255, '\n'}); err != nil {
		t.Fatal(err)
	}
	before, err := environment.Snapshot([]string{"/workspace/guard", "/workspace/should-stay-missing"})
	if err != nil {
		t.Fatal(err)
	}
	err = environment.WithTerminal(cli.Command{Args: []string{"bash", "-c", `saved=$(stty -g)
trap 'stty "$saved"; printf "\033[?25h\033[?1049l" >&2; exit 130' INT
stty raw -echo
printf '\033[?1049h\033[?25lready' >&2
printf literal:
dd bs=1 count=1 status=none
while :; do sleep 1; done`}}, cli.TerminalOptions{}, func(terminal *cli.Terminal) error {
		if _, err := terminal.Expect(cli.TerminalOutput, []byte("ready"), 3*time.Second); err != nil {
			return err
		}
		if _, err := terminal.SendInput([]byte{3}); err != nil {
			return err
		}
		observed, err := terminal.Expect(cli.TerminalStdout, []byte{'l', 'i', 't', 'e', 'r', 'a', 'l', ':', 3}, 3*time.Second)
		if err != nil {
			return err
		}
		if observed.Status.Kind != "running" {
			return fmt.Errorf("literal Ctrl-C terminated application: %+v", observed.Status)
		}
		if _, err := terminal.Signal("SIGINT"); err != nil {
			return err
		}
		observed, err = terminal.Wait(3 * time.Second)
		if err != nil {
			return err
		}
		if observed.Status != (cli.TerminalStatus{Kind: "exited", ExitCode: 130}) || !observed.TermiosRestored || !bytes.HasSuffix(observed.Terminal, []byte("\x1b[?25h\x1b[?1049l")) {
			return fmt.Errorf("SIGINT cancellation did not restore terminal: %+v", observed)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckSnapshot(before); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalLongOutputScrollAndTinyRecoveryScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	installDriver(t, environment)
	if err := environment.WriteFile("/workspace/scroll-guard", []byte("unchanged")); err != nil {
		t.Fatal(err)
	}
	before, err := environment.Snapshot([]string{"/workspace/scroll-guard", "/workspace/scroll-no-output"})
	if err != nil {
		t.Fatal(err)
	}
	err = environment.WithTerminal(cli.Command{Args: []string{"bash", "-c", `saved=$(stty -g)
trap 'if [ "$(stty size)" = "1 1" ]; then printf "tiny" >&2; else printf "recovered" >&2; fi' WINCH
stty raw -echo
for ((line=1; line<=120; line++)); do printf 'line:%03d\r\n' "$line" >&2; done
printf ready >&2
while :; do
  key=; IFS= read -r -N3 key || continue
  case "$key" in
    $'\033[A') printf '\033[Hpage:119' >&2 ;;
    $'\033[B') printf '\033[Hpage:120' >&2 ;;
    qqq) break ;;
  esac
done
stty "$saved"
printf '\033[?25h' >&2`}}, cli.TerminalOptions{Rows: 24, Columns: 80}, func(terminal *cli.Terminal) error {
		if _, err := terminal.Expect(cli.TerminalOutput, []byte("line:120\r\nready"), 3*time.Second); err != nil {
			return err
		}
		for _, step := range []struct {
			keys   []byte
			output string
		}{{[]byte("\x1b[A"), "\x1b[Hpage:119"}, {[]byte("\x1b[B"), "\x1b[Hpage:120"}} {
			if _, err := terminal.SendInput(step.keys); err != nil {
				return err
			}
			if _, err := terminal.Expect(cli.TerminalOutput, []byte(step.output), 3*time.Second); err != nil {
				return err
			}
		}
		for _, step := range []struct {
			rows, columns int
			output        string
		}{{1, 1, "tiny"}, {24, 80, "recovered"}} {
			if _, err := terminal.Resize(step.rows, step.columns); err != nil {
				return err
			}
			if _, err := terminal.SendInput([]byte("xxx")); err != nil {
				return err
			}
			if _, err := terminal.Expect(cli.TerminalOutput, []byte(step.output), 3*time.Second); err != nil {
				return err
			}
			if err := environment.CheckSnapshot(before); err != nil {
				return err
			}
		}
		if _, err := terminal.SendInput([]byte("\x1b[A")); err != nil {
			return err
		}
		if _, err := terminal.Expect(cli.TerminalOutput, []byte("\x1b[Hpage:119"), 3*time.Second); err != nil {
			return err
		}
		if _, err := terminal.SendInput([]byte("qqq")); err != nil {
			return err
		}
		observed, err := terminal.Wait(3 * time.Second)
		if err != nil {
			return err
		}
		var wantPrefix bytes.Buffer
		for line := 1; line <= 120; line++ {
			if _, err := fmt.Fprintf(&wantPrefix, "line:%03d\r\n", line); err != nil {
				return err
			}
		}
		if !bytes.HasPrefix(observed.Terminal, wantPrefix.Bytes()) || observed.Status != (cli.TerminalStatus{Kind: "exited"}) || !observed.TermiosRestored || !bytes.HasSuffix(observed.Terminal, []byte("\x1b[?25h")) {
			return fmt.Errorf("long-output recovery observation=%+v", observed)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckSnapshot(before); err != nil {
		t.Fatal(err)
	}
}
