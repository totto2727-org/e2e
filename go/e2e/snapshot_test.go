//go:build e2e

package e2e

import (
	"reflect"
	"testing"

	"github.com/totto2727-org/e2e/go/cli"
)

func TestFilesystemSnapshotScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	installDriver(t, environment)
	for _, path := range []string{"/workspace/b deleted", "/workspace/c bytes", "/workspace/d type"} {
		if err := environment.WriteFile(path, []byte{0, 255, '\n'}); err != nil {
			t.Fatal(err)
		}
	}
	if err := environment.WriteFile("/workspace/i json", []byte(`{"same":true}`)); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"mkdir", "/workspace/f directory"},
		{"ln", "-s", "../missing target\n", "/workspace/e dangling"},
		{"ln", "-s", "c bytes", "/workspace/h link"},
	} {
		if err := environment.CheckCommand(cli.Command{Args: args}, cli.Result{}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := environment.Snapshot([]string{
		"/workspace/i json", "/workspace/h link", "/workspace/g missing", "/workspace/f directory", "/workspace/e dangling",
		"/workspace/d type", "/workspace/c bytes", "/workspace/b deleted", "/workspace/a created ; literal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckSnapshot(before); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"rm", "/workspace/b deleted", "/workspace/d type", "/workspace/e dangling", "/workspace/h link"},
		{"mkdir", "/workspace/d type"},
		{"ln", "-s", "../other target\n", "/workspace/e dangling"},
		{"ln", "-s", "./c bytes", "/workspace/h link"},
		{"chmod", "700", "/workspace/f directory"},
	} {
		if err := environment.CheckCommand(cli.Command{Args: args}, cli.Result{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []struct {
		path    string
		content []byte
	}{
		{path: "/workspace/a created ; literal", content: nil},
		{path: "/workspace/c bytes", content: []byte{0, 254, '\n'}},
		{path: "/workspace/f directory/unselected child", content: []byte("not recursive")},
		{path: "/workspace/i json", content: []byte("{\n  \"same\": true\n}\n")},
	} {
		if err := environment.WriteFile(file.path, file.content); err != nil {
			t.Fatal(err)
		}
	}
	want := []cli.SnapshotDifference{
		{Path: "/workspace/a created ; literal", Change: cli.SnapshotExistence, BeforeKind: cli.PathMissing, AfterKind: cli.PathFile},
		{Path: "/workspace/b deleted", Change: cli.SnapshotExistence, BeforeKind: cli.PathFile, AfterKind: cli.PathMissing},
		{Path: "/workspace/c bytes", Change: cli.SnapshotContent, BeforeKind: cli.PathFile, AfterKind: cli.PathFile},
		{Path: "/workspace/d type", Change: cli.SnapshotKind, BeforeKind: cli.PathFile, AfterKind: cli.PathDirectory},
		{Path: "/workspace/e dangling", Change: cli.SnapshotTarget, BeforeKind: cli.PathSymlink, AfterKind: cli.PathSymlink},
		{Path: "/workspace/h link", Change: cli.SnapshotTarget, BeforeKind: cli.PathSymlink, AfterKind: cli.PathSymlink},
		{Path: "/workspace/i json", Change: cli.SnapshotContent, BeforeKind: cli.PathFile, AfterKind: cli.PathFile},
	}
	for range 2 {
		got, err := environment.CompareSnapshot(before)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("differences=%+v want=%+v", got, want)
		}
	}
	if err := environment.CheckSnapshot(before); err == nil {
		t.Fatal("expected snapshot mismatch without baseline update")
	}
}

func TestFilesystemSnapshotRejectsUnsupportedKindScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	installDriver(t, environment)
	before, err := environment.Snapshot([]string{"/workspace/pipe"})
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckCommand(cli.Command{Args: []string{"mkfifo", "/workspace/pipe"}}, cli.Result{}); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.Snapshot([]string{"/workspace/pipe"}); err == nil {
		t.Fatal("unsupported FIFO accepted in snapshot")
	}
	if _, err := environment.CompareSnapshot(before); err == nil {
		t.Fatal("unsupported FIFO treated as a comparison result")
	}
}

func TestFilesystemSnapshotPermissionErrorsScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example-nonroot:local")
	installDriver(t, environment)
	before, err := environment.Snapshot([]string{"/tmp/blocked/absent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.CheckCommand(cli.Command{Args: []string{"sh", "-c", "mkdir /tmp/blocked; touch /tmp/unreadable; chmod 000 /tmp/blocked /tmp/unreadable"}}, cli.Result{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/tmp/blocked/absent", "/tmp/unreadable"} {
		if _, err := environment.Snapshot([]string{path}); err == nil {
			t.Fatalf("inaccessible path %q was treated as absent or readable", path)
		}
	}
	if _, err := environment.CompareSnapshot(before); err == nil {
		t.Fatal("inaccessible ancestor was treated as unchanged missing path")
	}
}
