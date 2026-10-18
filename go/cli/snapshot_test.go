package cli

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
	if _, err := (&Environment{}).CompareSnapshot(FilesystemSnapshot{}); err == nil {
		t.Fatal("zero snapshot accepted")
	}
	if _, err := (&Environment{}).Snapshot([]string{"path"}); err == nil {
		t.Fatal("snapshot without installed driver accepted")
	}
}

func TestSnapshot_compares_bytes_types_targets_and_existence_in_sorted_order(t *testing.T) {
	container := &snapshotProtocolContainer{entries: []snapshotWireFixture{
		{Path: "a created", Kind: "missing"},
		{Path: "b deleted", Kind: "file", Data: []int{0, 255, 10}},
		{Path: "c content ; $(literal)", Kind: "file", Data: []int{0, 255, 10}},
		{Path: "d type", Kind: "file"},
		{Path: "e dangling", Kind: "symlink", Target: []int{'.', '.', '/', 'm', '\n', 255}},
		{Path: "f directory", Kind: "directory"},
		{Path: "g missing", Kind: "missing"},
		{Path: "h file link", Kind: "symlink", Target: []int{'c'}},
	}}
	environment := &Environment{ctx: t.Context(), container: container, driverPath: "/driver"}
	selected := []string{"h file link", "g missing", "f directory", "e dangling", "d type", "c content ; $(literal)", "b deleted", "a created"}
	before, err := environment.Snapshot(selected)
	if err != nil {
		t.Fatal(err)
	}
	selected[0] = "caller mutation"
	if err := environment.CheckSnapshot(before); err != nil {
		t.Fatal(err)
	}
	container.entries[0].Kind = "file"
	container.entries[1].Kind, container.entries[1].Data = "missing", nil
	container.entries[2].Data[1] = 254
	container.entries[3].Kind = "directory"
	container.entries[4].Target = []int{'o', 't', 'h', 'e', 'r'}
	want := []SnapshotDifference{
		{Path: "a created", Change: SnapshotExistence, BeforeKind: PathMissing, AfterKind: PathFile},
		{Path: "b deleted", Change: SnapshotExistence, BeforeKind: PathFile, AfterKind: PathMissing},
		{Path: "c content ; $(literal)", Change: SnapshotContent, BeforeKind: PathFile, AfterKind: PathFile},
		{Path: "d type", Change: SnapshotKind, BeforeKind: PathFile, AfterKind: PathDirectory},
		{Path: "e dangling", Change: SnapshotTarget, BeforeKind: PathSymlink, AfterKind: PathSymlink},
	}
	for range 2 {
		got, err := environment.CompareSnapshot(before)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("differences=%+v want=%+v", got, want)
		}
		got[0].Path = "caller-mutated-difference"
	}
	if err := environment.CheckSnapshot(before); err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("err=%v want snapshot mismatch", err)
	}
	if !reflect.DeepEqual(container.paths, []string{"a created", "b deleted", "c content ; $(literal)", "d type", "e dangling", "f directory", "g missing", "h file link"}) {
		t.Fatalf("selected paths=%q", container.paths)
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
			before := FilesystemSnapshot{entries: map[string]snapshotEntry{"path": {kind: PathMissing}}}
			for _, operation := range []func() error{
				func() error { _, err := environment.Snapshot([]string{"path"}); return err },
				func() error { _, err := environment.CompareSnapshot(before); return err },
				func() error { return environment.CheckSnapshot(before) },
			} {
				err := operation()
				if err == nil || (test.wantCause != nil && !errors.Is(err, test.wantCause)) {
					t.Fatalf("err=%v wantCause=%v", err, test.wantCause)
				}
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
