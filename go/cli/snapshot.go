package cli

import (
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
	PathOther     PathKind = "other"
)

// FilesystemSnapshot is an immutable, in-memory capture of explicit paths.
// Its zero value contains no paths. No directory traversal or metadata is captured.
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

// SnapshotEntry contains one selected path's exact state.
// Content and Target preserve raw bytes without UTF-8 conversion.
type SnapshotEntry struct {
	Path    string
	Kind    PathKind
	Content string
	Target  string
}

// Entries returns a fresh slice ordered lexicographically by path.
// The immutable strings and copied slice cannot mutate captured state.
func (s FilesystemSnapshot) Entries() []SnapshotEntry {
	entries := make([]SnapshotEntry, 0, len(s.entries))
	for path, entry := range s.entries {
		entries = append(entries, SnapshotEntry{Path: path, Kind: entry.kind, Content: string(entry.content), Target: string(entry.target)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}
