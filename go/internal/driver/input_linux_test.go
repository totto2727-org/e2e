package driver

import (
	"bytes"
	"testing"
)

func TestTerminalSendAllRawBytes(t *testing.T) {
	terminal := terminalFor(t, `stty raw -echo < /dev/tty; printf ready >&2; head -c 131072`, nil)
	perform(t, terminal, map[string]any{"operation": "expect", "channel": "terminal", "data": numbers([]byte("ready"))})
	data := bytes.Repeat([]byte{0, 3, 4, 13, 10, 127, 128, 255}, 16384)
	perform(t, terminal, map[string]any{"operation": "send", "data": numbers(data)})
	observed := perform(t, terminal, map[string]any{"operation": "wait"})
	if !bytes.Equal(intsBytes(observed.Stdout), data) {
		t.Fatalf("raw PTY send dropped/changed bytes: got %d want %d", len(observed.Stdout), len(data))
	}
	if observed.Status.Code == nil || *observed.Status.Code != 0 {
		t.Fatalf("raw send child status: %#v", observed.Status)
	}
}

func TestTerminalCanonicalEOFIsLiteralInput(t *testing.T) {
	terminal := terminalFor(t, `exec cat`, nil)
	perform(t, terminal, map[string]any{"operation": "send", "data": []int{65, 10, 4}})
	observed := perform(t, terminal, map[string]any{"operation": "wait"})
	if string(intsBytes(observed.Stdout)) != "A\n" || observed.Status.Code == nil || *observed.Status.Code != 0 {
		t.Fatalf("canonical EOF: %#v", observed)
	}
}

func TestTerminalRejectsInvalidArguments(t *testing.T) {
	for _, arguments := range []any{nil, []any{}, []any{"sh", nil}, []any{"sh", true}, []string{""}, []string{"sh", "\x00"}} {
		if _, err := newTerminal(req(t, map[string]any{"argv": arguments})); err == nil {
			t.Fatalf("accepted invalid arguments: %#v", arguments)
		}
	}
}
