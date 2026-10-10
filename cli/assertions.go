package cli

import "fmt"

// CommandExpectation compares a command's exit code and output streams exactly.
type CommandExpectation struct {
	Command  Command
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *Environment) CheckCommand(expectation CommandExpectation) error {
	result, err := e.RunSeparated(expectation.Command)
	if err != nil {
		return err
	}
	if result.ExitCode != expectation.ExitCode || result.Stdout != expectation.Stdout || result.Stderr != expectation.Stderr {
		return fmt.Errorf("command %q: exit_code=%d stdout=%q stderr=%q want_exit_code=%d want_stdout=%q want_stderr=%q", expectation.Command.Args, result.ExitCode, result.Stdout, result.Stderr, expectation.ExitCode, expectation.Stdout, expectation.Stderr)
	}
	return nil
}

// CheckMissing rejects existing paths, including dangling symbolic links.
func (e *Environment) CheckMissing(path string) error {
	exists, err := e.probePath("-e", path)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("path %q: expected missing path", path)
	}
	linked, err := e.probePath("-L", path)
	if err != nil {
		return err
	}
	if linked {
		return fmt.Errorf("path %q: expected missing path, found symbolic link", path)
	}
	return nil
}

// CheckRegularFile requires a regular file, not a symbolic link to one.
func (e *Environment) CheckRegularFile(path string) error {
	return e.checkPhysicalType("-f", "regular file", path)
}

// CheckDirectory requires a directory, not a symbolic link to one.
func (e *Environment) CheckDirectory(path string) error {
	return e.checkPhysicalType("-d", "directory", path)
}

// CheckSymlink compares the literal link target without resolving it.
func (e *Environment) CheckSymlink(path, literalTarget string) error {
	linked, err := e.probePath("-L", path)
	if err != nil {
		return err
	}
	if !linked {
		return fmt.Errorf("path %q: expected symbolic link", path)
	}
	result, err := e.RunSeparated(Command{Args: []string{"readlink", "--", path}})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("readlink %q: exit_code=%d stderr=%q", path, result.ExitCode, result.Stderr)
	}
	if result.Stdout != literalTarget+"\n" {
		return fmt.Errorf("symlink %q: target_output=%q want=%q", path, result.Stdout, literalTarget+"\n")
	}
	return nil
}

func (e *Environment) checkPhysicalType(flag, description, path string) error {
	matches, err := e.probePath(flag, path)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("path %q: expected %s", path, description)
	}
	linked, err := e.probePath("-L", path)
	if err != nil {
		return err
	}
	if linked {
		return fmt.Errorf("path %q: expected physical %s, found symbolic link", path, description)
	}
	return nil
}

func (e *Environment) probePath(flag, path string) (bool, error) {
	result, err := e.RunSeparated(Command{Args: []string{"test", flag, path}})
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
