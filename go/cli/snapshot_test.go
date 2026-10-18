package cli

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSnapshot_rejects_invalid_paths_before_execution(t *testing.T) {
	for _, paths := range [][]string{nil, {}, {""}, {"valid", "bad\x00path"}, {"same", "same"}} {
		t.Run(strings.Join(paths, ","), func(t *testing.T) {
			container := &fakeContainer{}
			environment := &Environment{ctx: t.Context(), container: container, driverPath: "/driver"}
			if _, err := environment.Snapshot(paths); err == nil {
				t.Fatal("expected invalid path error")
			}
			if container.runCalls != 0 {
				t.Fatalf("invalid input invoked runtime %d times", container.runCalls)
			}
		})
	}
	if _, err := (&Environment{}).Snapshot([]string{"path"}); err == nil {
		t.Fatal("snapshot without installed driver accepted")
	}
}

func TestSnapshot_entries_preserve_bytes_and_sorted_immutable_state(t *testing.T) {
	container := &snapshotProtocolContainer{entries: []snapshotWireFixture{
		{Path: "file", Kind: "file", Data: []int{0, 255, 10}},
		{Path: "dangling", Kind: "symlink", Target: []int{'.', '.', '/', 'm', '\n', 255}},
		{Path: "directory", Kind: "directory"},
		{Path: "missing", Kind: "missing"},
	}}
	environment := &Environment{ctx: t.Context(), container: container, driverPath: "/driver"}
	selected := []string{"missing", "file", "directory", "dangling"}
	before, err := environment.Snapshot(selected)
	if err != nil {
		t.Fatal(err)
	}
	selected[0] = "caller mutation"
	want := []SnapshotEntry{
		{Path: "dangling", Kind: PathSymlink, Target: "../m\n\xff"},
		{Path: "directory", Kind: PathDirectory},
		{Path: "file", Kind: PathFile, Content: "\x00\xff\n"},
		{Path: "missing", Kind: PathMissing},
	}
	for range 2 {
		got := before.Entries()
		if !slices.Equal(got, want) {
			t.Fatalf("entries=%#v want=%#v", got, want)
		}
		got[0] = SnapshotEntry{Path: "caller mutation"}
	}
	container.entries[0].Data[1] = 254
	after, err := environment.Snapshot([]string{"missing", "file", "directory", "dangling"})
	if err != nil {
		t.Fatal(err)
	}
	want[2].Content = "\x00\xfe\n"
	if got := after.Entries(); !slices.Equal(got, want) {
		t.Fatalf("entries=%#v want=%#v", got, want)
	}
	if got := before.Entries()[2].Content; got != "\x00\xff\n" {
		t.Fatalf("captured bytes changed: %q", got)
	}
	if got := (FilesystemSnapshot{}).Entries(); len(got) != 0 {
		t.Fatalf("zero snapshot entries=%#v", got)
	}
}

func TestSnapshot_rejects_errors_and_malformed_or_unsupported_states(t *testing.T) {
	cause := errors.New("transport failure")
	for _, test := range []struct {
		name      string
		container *fakeContainer
		wantCause error
	}{
		{name: "transport", container: &fakeContainer{runErr: cause}, wantCause: cause},
		{name: "permission denied", container: &fakeContainer{result: Result{ExitCode: 1, Stdout: `{"ok":false,"error":"permission denied"}`}}},
		{name: "read error", container: &fakeContainer{result: Result{ExitCode: 1, Stdout: `{"ok":false,"error":"read failed"}`}}},
		{name: "invalid envelope", container: &fakeContainer{result: Result{Stdout: "{"}}},
		{name: "no paths", container: &fakeContainer{result: Result{Stdout: `{"ok":true,"value":[]}`}}},
		{name: "unsupported kind", container: &fakeContainer{result: Result{Stdout: `{"ok":true,"value":[{"path":"path","kind":"fifo","data":[],"target":[]}]}`}}},
		{name: "missing arrays", container: &fakeContainer{result: Result{Stdout: `{"ok":true,"value":[{"path":"path","kind":"missing"}]}`}}},
		{name: "invalid bytes", container: &fakeContainer{result: Result{Stdout: `{"ok":true,"value":[{"path":"path","kind":"file","data":[256],"target":[]}]}`}}},
		{name: "unselected path", container: &fakeContainer{result: Result{Stdout: `{"ok":true,"value":[{"path":"other","kind":"missing","data":[],"target":[]}]}`}}},
		{name: "missing with content", container: &fakeContainer{result: Result{Stdout: `{"ok":true,"value":[{"path":"path","kind":"missing","data":[1],"target":[]}]}`}}},
		{name: "file with target", container: &fakeContainer{result: Result{Stdout: `{"ok":true,"value":[{"path":"path","kind":"file","data":[],"target":[1]}]}`}}},
		{name: "empty symlink target", container: &fakeContainer{result: Result{Stdout: `{"ok":true,"value":[{"path":"path","kind":"symlink","data":[],"target":[]}]}`}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := &Environment{ctx: t.Context(), container: test.container, driverPath: "/driver"}
			_, err := environment.Snapshot([]string{"path"})
			if err == nil || (test.wantCause != nil && !errors.Is(err, test.wantCause)) {
				t.Fatalf("err=%v wantCause=%v", err, test.wantCause)
			}
		})
	}
}

type snapshotWireFixture struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Data   []int  `json:"data"`
	Target []int  `json:"target"`
}
type snapshotProtocolContainer struct {
	fakeContainer
	entries []snapshotWireFixture
	paths   []string
}

func (c *snapshotProtocolContainer) Run(_ context.Context, command Command) (Result, error) {
	var request struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal([]byte(command.Args[2]), &request); err != nil {
		return Result{}, err
	}
	c.paths = request.Paths
	for index := range c.entries {
		if c.entries[index].Data == nil {
			c.entries[index].Data = []int{}
		}
		if c.entries[index].Target == nil {
			c.entries[index].Target = []int{}
		}
	}
	encoded, err := json.Marshal(struct {
		OK    bool                  `json:"ok"`
		Value []snapshotWireFixture `json:"value"`
	}{true, c.entries})
	return Result{Stdout: string(encoded)}, err
}
