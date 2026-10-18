package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// PathKind is the physical kind of an explicitly selected snapshot path.
type PathKind string

const (
	PathMissing   PathKind = "missing"
	PathFile      PathKind = "file"
	PathDirectory PathKind = "directory"
	PathSymlink   PathKind = "symlink"
)

// SnapshotChange identifies the part of a path that changed.
type SnapshotChange string

const (
	SnapshotExistence SnapshotChange = "existence"
	SnapshotKind      SnapshotChange = "kind"
	SnapshotContent   SnapshotChange = "content"
	SnapshotTarget    SnapshotChange = "target"
)

// SnapshotDifference describes a change from a captured state to current state.
// Differences are ordered lexicographically by Path.
type SnapshotDifference struct {
	Path       string
	Change     SnapshotChange
	BeforeKind PathKind
	AfterKind  PathKind
}

// FilesystemSnapshot is an immutable, in-memory capture of explicit paths.
// Its zero value is invalid. No directory traversal or metadata is captured.
type FilesystemSnapshot struct {
	entries map[string]snapshotEntry
}

type snapshotEntry struct {
	kind    PathKind
	content []byte
	target  []byte
}

// Snapshot captures only the supplied paths. Paths must be nonempty, unique,
// and contain no NUL. InstallDriver is required before capture.
func (e *Environment) Snapshot(paths []string) (FilesystemSnapshot, error) {
	if len(paths) == 0 {
		return FilesystemSnapshot{}, errors.New("snapshot requires at least one path")
	}
	selected := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" || strings.ContainsRune(path, '\x00') {
			return FilesystemSnapshot{}, fmt.Errorf("snapshot path %q: must be nonempty and contain no NUL", path)
		}
		if _, duplicate := selected[path]; duplicate {
			return FilesystemSnapshot{}, fmt.Errorf("snapshot path %q: duplicate path", path)
		}
		selected[path] = struct{}{}
	}
	ordered := make([]string, 0, len(selected))
	for path := range selected {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	value, err := e.driverCall("snapshot", struct {
		Paths []string `json:"paths"`
	}{ordered}, Command{})
	if err != nil {
		return FilesystemSnapshot{}, err
	}
	var wire []struct {
		Path   string       `json:"path"`
		Kind   PathKind     `json:"kind"`
		Data   *driverBytes `json:"data"`
		Target *driverBytes `json:"target"`
	}
	if err := json.Unmarshal(value, &wire); err != nil {
		return FilesystemSnapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	if len(wire) != len(selected) {
		return FilesystemSnapshot{}, errors.New("snapshot response does not cover selected paths")
	}
	entries := make(map[string]snapshotEntry, len(wire))
	for _, item := range wire {
		if _, exists := selected[item.Path]; !exists {
			return FilesystemSnapshot{}, fmt.Errorf("snapshot returned unselected path %q", item.Path)
		}
		if _, duplicate := entries[item.Path]; duplicate {
			return FilesystemSnapshot{}, fmt.Errorf("snapshot returned duplicate path %q", item.Path)
		}
		if item.Data == nil || item.Target == nil {
			return FilesystemSnapshot{}, fmt.Errorf("snapshot path %q: missing byte arrays", item.Path)
		}
		entry := snapshotEntry{kind: item.Kind, content: []byte(*item.Data), target: []byte(*item.Target)}
		switch item.Kind {
		case PathMissing, PathDirectory:
			if len(entry.content) != 0 || len(entry.target) != 0 {
				return FilesystemSnapshot{}, fmt.Errorf("snapshot path %q: unexpected contents", item.Path)
			}
		case PathFile:
			if len(entry.target) != 0 {
				return FilesystemSnapshot{}, fmt.Errorf("snapshot file %q: unexpected target", item.Path)
			}
		case PathSymlink:
			if len(entry.content) != 0 || len(entry.target) == 0 {
				return FilesystemSnapshot{}, fmt.Errorf("snapshot symlink %q: invalid target or contents", item.Path)
			}
		default:
			return FilesystemSnapshot{}, fmt.Errorf("snapshot path %q: unsupported physical kind %q", item.Path, item.Kind)
		}
		entries[item.Path] = entry
	}
	return FilesystemSnapshot{entries: entries}, nil
}

// CompareSnapshot captures the same paths again and returns sorted differences.
// Read failures and unsupported physical kinds are errors, never differences.
func (e *Environment) CompareSnapshot(before FilesystemSnapshot) ([]SnapshotDifference, error) {
	if len(before.entries) == 0 {
		return nil, errors.New("cannot compare an uninitialized filesystem snapshot")
	}
	paths := make([]string, 0, len(before.entries))
	for path := range before.entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	after, err := e.Snapshot(paths)
	if err != nil {
		return nil, err
	}
	differences := make([]SnapshotDifference, 0)
	for _, path := range paths {
		old, current := before.entries[path], after.entries[path]
		var change SnapshotChange
		switch {
		case old.kind != current.kind && (old.kind == PathMissing || current.kind == PathMissing):
			change = SnapshotExistence
		case old.kind != current.kind:
			change = SnapshotKind
		case old.kind == PathFile && !bytes.Equal(old.content, current.content):
			change = SnapshotContent
		case old.kind == PathSymlink && !bytes.Equal(old.target, current.target):
			change = SnapshotTarget
		default:
			continue
		}
		differences = append(differences, SnapshotDifference{Path: path, Change: change, BeforeKind: old.kind, AfterKind: current.kind})
	}
	return differences, nil
}

// CheckSnapshot requires every selected path to remain unchanged.
func (e *Environment) CheckSnapshot(before FilesystemSnapshot) error {
	differences, err := e.CompareSnapshot(before)
	if err != nil {
		return err
	}
	if len(differences) != 0 {
		return fmt.Errorf("filesystem snapshot changed: %+v", differences)
	}
	return nil
}
