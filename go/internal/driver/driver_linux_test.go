package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(Run(os.Args[1:], os.Stdout))
	}
	os.Exit(m.Run())
}
func req(t *testing.T, value any) request {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parse(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func terminalFor(t *testing.T, script string, input any) *terminal {
	t.Helper()
	r := req(t, map[string]any{"argv": []string{"/bin/sh", "-c", script}, "input": input, "timeout": 5})
	terminal, err := newTerminal(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := terminal.close(); err != nil {
			t.Error(err)
		}
	})
	return terminal
}
func perform(t *testing.T, terminal *terminal, value any) observation {
	t.Helper()
	observed, err := terminal.perform(req(t, value))
	if err != nil {
		t.Fatal(err)
	}
	return observed
}
func intsBytes(values []int) []byte {
	data := make([]byte, len(values))
	for i, v := range values {
		data[i] = byte(v)
	}
	return data
}

func TestSnapshotPhysicalPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	link := filepath.Join(dir, "link")
	missing := filepath.Join(dir, "missing")
	data := []byte{0, 1, 255, 13, 10}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	target := "../literal\xff/missing"
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	entries, err := snapshot(req(t, map[string]any{"paths": []string{missing, link, file, file, dir}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries: %#v", entries)
	}
	if entries[0].Path != dir || entries[0].Kind != "directory" || entries[1].Kind != "file" || !bytes.Equal(intsBytes(entries[1].Data), data) || entries[2].Kind != "symlink" || !bytes.Equal(intsBytes(entries[2].Target), []byte(target)) || entries[3].Kind != "missing" {
		t.Fatalf("physical snapshot: %#v", entries)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot(req(t, map[string]any{"paths": []string{fifo}})); err == nil {
		t.Fatal("accepted special file")
	}
	if _, err := snapshot(req(t, map[string]any{"paths": []string{file + "/child"}})); err == nil {
		t.Fatal("treated ENOTDIR as missing")
	}
}
func TestTerminalSeparateStreamsAndApplicationFailure(t *testing.T) {
	terminal := terminalFor(t, `test -t 0 && test -t 2 && test ! -t 1 || exit 9; printf '\000\377out'; printf 'err\n' >&2; exit 7`, nil)
	observed := perform(t, terminal, map[string]any{"operation": "wait"})
	if observed.Status.Kind != "exited" || observed.Status.Code == nil || *observed.Status.Code != 7 {
		t.Fatalf("application failure status: %#v", observed.Status)
	}
	if !bytes.Equal(intsBytes(observed.Stdout), []byte{0, 255, 'o', 'u', 't'}) || string(intsBytes(observed.Terminal)) != "err\r\n" {
		t.Fatalf("separate streams: %#v", observed)
	}
	if !observed.Restored || len(observed.Initial) != 7 || len(observed.Initial[6].([]int)) != 32 {
		t.Fatalf("termios: %#v", observed)
	}
}
func TestTerminalExpectCursorAcrossReads(t *testing.T) {
	terminal := terminalFor(t, `printf ab >&2; sleep 0.05; printf cabcTAIL >&2; read answer; printf '%s' "$answer"`, nil)
	for range 2 {
		perform(t, terminal, map[string]any{"operation": "expect", "channel": "terminal", "data": numbers([]byte("abc"))})
	}
	if terminal.cursors["terminal"] != 6 {
		t.Fatalf("cursor %d", terminal.cursors["terminal"])
	}
	perform(t, terminal, map[string]any{"operation": "expect", "channel": "terminal", "data": numbers([]byte("TAIL"))})
	perform(t, terminal, map[string]any{"operation": "send", "data": numbers([]byte("answer\n"))})
	observed := perform(t, terminal, map[string]any{"operation": "wait"})
	if string(intsBytes(observed.Stdout)) != "answer" {
		t.Fatalf("send: %#v", observed)
	}
	_, err := terminal.perform(req(t, map[string]any{"operation": "expect", "channel": "terminal", "data": numbers([]byte("abc"))}))
	if err == nil || !strings.Contains(err.Error(), "terminal") || !strings.Contains(err.Error(), "[97 98 99]") {
		t.Fatalf("premature-exit diagnostic: %v", err)
	}
}
func TestTerminalFinitePipeAndUnreadInput(t *testing.T) {
	input := bytes.Repeat([]byte("0123456789"), 20000)
	terminal := terminalFor(t, `test ! -t 0 && test -t 2 || exit 9; dd bs=1 count=3 2>/dev/null`, numbers(input))
	observed := perform(t, terminal, map[string]any{"operation": "wait"})
	if string(intsBytes(observed.Stdout)) != "012" || !bytes.Equal(intsBytes(observed.UnreadInput), input[3:]) {
		t.Fatalf("pipe output %v unread %d want %d", observed.Stdout, len(observed.UnreadInput), len(input)-3)
	}
	if _, err := terminal.perform(req(t, map[string]any{"operation": "send", "data": []int{1}})); err == nil {
		t.Fatal("accepted send with pipe stdin")
	}
	terminal = terminalFor(t, `cat`, numbers(input))
	observed = perform(t, terminal, map[string]any{"operation": "wait"})
	if !bytes.Equal(intsBytes(observed.Stdout), input) || len(observed.UnreadInput) != 0 {
		t.Fatalf("finite pipe must provide all bytes + EOF: stdout %d unread %d", len(observed.Stdout), len(observed.UnreadInput))
	}
}
func TestTerminalResizeAndSignal(t *testing.T) {
	terminal := terminalFor(t, `trap 'stty size < /dev/tty >&2; exit 0' WINCH; printf ready >&2; while :; do sleep 0.01; done`, nil)
	perform(t, terminal, map[string]any{"operation": "expect", "channel": "terminal", "data": numbers([]byte("ready"))})
	perform(t, terminal, map[string]any{"operation": "resize", "rows": 17, "columns": 61})
	observed := perform(t, terminal, map[string]any{"operation": "wait"})
	if !strings.Contains(string(intsBytes(observed.Terminal)), "17 61\r\n") {
		t.Fatalf("resize: %q", intsBytes(observed.Terminal))
	}
	terminal = terminalFor(t, `exec sleep 10`, nil)
	perform(t, terminal, map[string]any{"operation": "signal", "name": "SIGTERM"})
	observed = perform(t, terminal, map[string]any{"operation": "wait"})
	if observed.Status.Kind != "signaled" || observed.Status.Signal != int(unix.SIGTERM) {
		t.Fatalf("signal: %#v", observed.Status)
	}
}
func TestTerminalTermiosRestoration(t *testing.T) {
	terminal := terminalFor(t, `saved=$(stty -g < /dev/tty); stty -echo -icanon < /dev/tty; printf raw >&2; dd bs=1 count=1 >/dev/null 2>/dev/null; stty "$saved" < /dev/tty`, nil)
	observed := perform(t, terminal, map[string]any{"operation": "expect", "channel": "terminal", "data": numbers([]byte("raw"))})
	if observed.Restored {
		t.Fatal("raw termios reported restored")
	}
	perform(t, terminal, map[string]any{"operation": "send", "data": []int{120}})
	observed = perform(t, terminal, map[string]any{"operation": "wait"})
	if !observed.Restored || !reflect.DeepEqual(observed.Initial, observed.Current) {
		t.Fatal("restored terminal not observed")
	}
}
func TestSessionReadinessCloseAndWatchdog(t *testing.T) {
	for _, watchdog := range []bool{false, true} {
		t.Run(fmt.Sprint(watchdog), func(t *testing.T) {
			timeout := 5.0
			if watchdog {
				timeout = 0.3
			}
			result := start(req(t, map[string]any{"argv": []string{"sh", "-c", "echo $$; exec sleep 10"}, "timeout": timeout}))
			if !result.OK {
				t.Fatalf("start: %#v", result)
			}
			value, ok := result.Value.(map[string]any)
			if !ok {
				t.Fatalf("start value: %#v", result.Value)
			}
			path := value["path"].(string)
			t.Cleanup(func() { requestSession(req(t, map[string]any{"path": path, "operation": "close"})) })
			info, err := os.Stat(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0700 {
				t.Fatalf("directory permissions %o", info.Mode().Perm())
			}
			result = requestSession(req(t, map[string]any{"path": path, "operation": "expect", "channel": "stdout", "data": []int{10}, "timeout": 1}))
			if !result.OK {
				t.Fatalf("request: %#v", result)
			}
			observed := result.Value.(map[string]any)
			stdout := observed["stdout"].([]any)
			var pid int
			var pidText strings.Builder
			for _, v := range stdout {
				pidText.WriteByte(byte(v.(float64)))
			}
			if _, err := fmt.Sscanf(pidText.String(), "%d", &pid); err != nil {
				t.Fatal(err)
			}
			if watchdog {
				deadline := time.Now().Add(2 * time.Second)
				for {
					_, err := os.Stat(filepath.Dir(path))
					if errors.Is(err, os.ErrNotExist) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("watchdog did not remove session directory")
					}
					time.Sleep(10 * time.Millisecond)
				}
			} else {
				result = requestSession(req(t, map[string]any{"path": path, "operation": "close"}))
				if !result.OK || result.Value != nil {
					t.Fatalf("close: %#v", result)
				}
				if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("close acknowledged before cleanup: %v", err)
				}
			}
			if err := unix.Kill(pid, 0); !errors.Is(err, unix.ESRCH) {
				t.Fatalf("owned child survived cleanup: %v", err)
			}
			result = requestSession(req(t, map[string]any{"path": path, "operation": "close"}))
			if !result.OK {
				t.Fatalf("repeated close: %#v", result)
			}
			result = requestSession(req(t, map[string]any{"path": path, "operation": "observe"}))
			if result.OK {
				t.Fatal("observe succeeded after close")
			}
		})
	}
}
func TestTerminalTimeoutBounded(t *testing.T) {
	terminal := terminalFor(t, `printf partial >&2; exec sleep 10`, nil)
	start := time.Now()
	_, err := terminal.perform(req(t, map[string]any{"operation": "expect", "channel": "terminal", "data": numbers([]byte("never")), "timeout": 0.05}))
	if err == nil || !strings.Contains(err.Error(), "terminal") || time.Since(start) > time.Second {
		t.Fatalf("expect deadline: %v", err)
	}
	observed, err := terminal.observe()
	if err != nil {
		t.Fatal(err)
	}
	if string(intsBytes(observed.Terminal)) != "partial" || observed.Status.Kind != "running" {
		t.Fatalf("partial observation: %#v", observed)
	}
}
