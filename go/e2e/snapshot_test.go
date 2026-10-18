//go:build e2e

package e2e

import (
	"slices"
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
		if result, err := environment.Run(cli.Command{Args: args}); err != nil {
			t.Fatal(err)
		} else if want := (cli.Result{}); result != want {
			t.Fatalf("result=%#v want=%#v", result, want)
		}
	}
	paths := []string{
		"/workspace/i json", "/workspace/h link", "/workspace/g missing", "/workspace/f directory", "/workspace/e dangling",
		"/workspace/d type", "/workspace/c bytes", "/workspace/b deleted", "/workspace/a created ; literal",
	}
	before, err := environment.Snapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	after, err := environment.Snapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := after.Entries(), before.Entries(); !slices.Equal(got, want) {
		t.Errorf("snapshot=%#v want=%#v", got, want)
	}
	for _, args := range [][]string{
		{"rm", "/workspace/b deleted", "/workspace/d type", "/workspace/e dangling", "/workspace/h link"},
		{"mkdir", "/workspace/d type"},
		{"ln", "-s", "../other target\n", "/workspace/e dangling"},
		{"ln", "-s", "./c bytes", "/workspace/h link"},
		{"chmod", "700", "/workspace/f directory"},
	} {
		if result, err := environment.Run(cli.Command{Args: args}); err != nil {
			t.Fatal(err)
		} else if want := (cli.Result{}); result != want {
			t.Fatalf("result=%#v want=%#v", result, want)
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
	want := []cli.SnapshotEntry{
		{Path: "/workspace/a created ; literal", Kind: cli.PathFile},
		{Path: "/workspace/b deleted", Kind: cli.PathMissing},
		{Path: "/workspace/c bytes", Kind: cli.PathFile, Content: "\x00\xfe\n"},
		{Path: "/workspace/d type", Kind: cli.PathDirectory},
		{Path: "/workspace/e dangling", Kind: cli.PathSymlink, Target: "../other target\n"},
		{Path: "/workspace/f directory", Kind: cli.PathDirectory},
		{Path: "/workspace/g missing", Kind: cli.PathMissing},
		{Path: "/workspace/h link", Kind: cli.PathSymlink, Target: "./c bytes"},
		{Path: "/workspace/i json", Kind: cli.PathFile, Content: "{\n  \"same\": true\n}\n"},
	}
	for range 2 {
		after, err := environment.Snapshot(paths)
		if err != nil {
			t.Fatal(err)
		}
		if got := after.Entries(); !slices.Equal(got, want) {
			t.Fatalf("snapshot=%#v want=%#v", got, want)
		}
	}
}

func TestFilesystemSnapshotRejectsUnsupportedKindScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example:local")
	installDriver(t, environment)
	paths := []string{"/workspace/pipe"}
	if _, err := environment.Snapshot(paths); err != nil {
		t.Fatal(err)
	}
	if result, err := environment.Run(cli.Command{Args: []string{"mkfifo", "/workspace/pipe"}}); err != nil {
		t.Fatal(err)
	} else if want := (cli.Result{}); result != want {
		t.Fatalf("result=%#v want=%#v", result, want)
	}
	if _, err := environment.Snapshot([]string{"/workspace/pipe"}); err == nil {
		t.Fatal("unsupported FIFO accepted in snapshot")
	}
}

func TestFilesystemSnapshotPermissionErrorsScenario(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "e2e-example-nonroot:local")
	installDriver(t, environment)
	paths := []string{"/tmp/blocked/absent"}
	if _, err := environment.Snapshot(paths); err != nil {
		t.Fatal(err)
	}
	if result, err := environment.Run(cli.Command{Args: []string{"sh", "-c", "mkdir /tmp/blocked; touch /tmp/unreadable; chmod 000 /tmp/blocked /tmp/unreadable"}}); err != nil {
		t.Fatal(err)
	} else if want := (cli.Result{}); result != want {
		t.Fatalf("result=%#v want=%#v", result, want)
	}
	for _, path := range []string{"/tmp/blocked/absent", "/tmp/unreadable"} {
		if _, err := environment.Snapshot([]string{path}); err == nil {
			t.Fatalf("inaccessible path %q was treated as absent or readable", path)
		}
	}
}
