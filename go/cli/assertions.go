package cli

import (
	"fmt"
	"strings"
)

// PathKind reports a physical path kind without following symbolic links.
// It does not require an installed driver.
func (e *Environment) PathKind(path string) (PathKind, error) {
	if err := validatePath(path); err != nil {
		return "", err
	}
	for _, probe := range []struct {
		flag string
		kind PathKind
	}{{"-L", PathSymlink}, {"-f", PathFile}, {"-d", PathDirectory}} {
		matches, err := e.probePath(probe.flag, path)
		if err != nil {
			return "", err
		}
		if matches {
			return probe.kind, nil
		}
	}
	exists, err := e.probePath("-e", path)
	if err != nil {
		return "", err
	}
	if exists {
		return PathOther, nil
	}
	return PathMissing, nil
}

// ReadSymlink returns a literal target without resolving it.
// Strings preserve exact target bytes, including trailing newlines.
func (e *Environment) ReadSymlink(path string) (string, error) {
	if err := validatePath(path); err != nil {
		return "", err
	}
	result, err := e.Run(Command{Args: []string{"readlink", "--", path}})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("readlink %q: exit_code=%d stderr=%q", path, result.ExitCode, result.Stderr)
	}
	if !strings.HasSuffix(result.Stdout, "\n") {
		return "", fmt.Errorf("readlink %q: missing output terminator", path)
	}
	return strings.TrimSuffix(result.Stdout, "\n"), nil
}

func validatePath(path string) error {
	if path == "" || strings.ContainsRune(path, '\x00') {
		return fmt.Errorf("path %q: must be nonempty and contain no NUL", path)
	}
	return nil
}

func (e *Environment) probePath(flag, path string) (bool, error) {
	result, err := e.Run(Command{Args: []string{"test", flag, path}})
	if err != nil {
		return false, err
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("test %s %q: unexpected exit_code=%d stderr=%q", flag, path, result.ExitCode, result.Stderr)
	}
}
