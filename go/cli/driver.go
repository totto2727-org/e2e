package cli

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// InstallDriver installs a caller-supplied static Linux driver for the image's
// architecture. Installation is scoped to this environment and cannot be replaced.
// Existing command and file APIs do not require a driver.
func (e *Environment) InstallDriver(binary []byte) (err error) {
	if len(binary) == 0 {
		return errors.New("driver executable must not be empty")
	}
	e.driverMu.Lock()
	defer e.driverMu.Unlock()
	if e.driverPath != "" {
		return errors.New("environment driver is already installed")
	}
	created, err := e.Run(Command{Args: []string{"mktemp", "-d", "/tmp/caseyard-driver-XXXXXXXX"}})
	if err != nil {
		return err
	}
	if created.ExitCode != 0 {
		return fmt.Errorf("create driver directory: exit=%d stderr=%q", created.ExitCode, created.Stderr)
	}
	directory := strings.TrimSuffix(created.Stdout, "\n")
	if !strings.HasPrefix(directory, "/tmp/caseyard-driver-") || path.Dir(directory) != "/tmp" || strings.ContainsAny(directory, "\x00\n\r") {
		return errors.New("driver installation returned an invalid private directory")
	}
	defer func() {
		if err != nil {
			removed, removeErr := e.Run(Command{Args: []string{"rm", "-rf", "--", directory}})
			if removeErr == nil && removed.ExitCode != 0 {
				removeErr = fmt.Errorf("remove driver directory: exit=%d stderr=%q", removed.ExitCode, removed.Stderr)
			}
			err = errors.Join(err, removeErr)
		}
	}()
	executable := directory + "/driver"
	if err := e.container.CopyToContainer(e.ctx, binary, executable, 0o755); err != nil {
		return fmt.Errorf("copy driver executable: %w", err)
	}
	e.driverPath = executable
	return nil
}

func (e *Environment) installedDriver() (string, error) {
	e.driverMu.Lock()
	defer e.driverMu.Unlock()
	if e.driverPath == "" {
		return "", errors.New("operation requires InstallDriver with a static Linux executable for the image architecture")
	}
	return e.driverPath, nil
}
