package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func dispatch(mode string, r request) reply {
	switch mode {
	case "snapshot":
		value, err := snapshot(r)
		if err != nil {
			return failure(err, nil)
		}
		return success(value)
	case "start":
		return start(r)
	case "request":
		return requestSession(r)
	case "serve":
		return serve(r)
	default:
		return failure(fmt.Errorf("unknown driver mode: %s", mode), nil)
	}
}

type pathEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Data   []int  `json:"data"`
	Target []int  `json:"target"`
}

func snapshot(r request) ([]pathEntry, error) {
	var paths []string
	if raw := r["paths"]; len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("paths must be an array")
	}
	if err := json.Unmarshal(r["paths"], &paths); err != nil {
		return nil, err
	}
	for _, path := range paths {
		if path == "" || strings.ContainsRune(path, 0) {
			return nil, fmt.Errorf("path must be a nonempty NUL-free string")
		}
	}
	sort.Strings(paths)
	result := make([]pathEntry, 0, len(paths))
	for i, path := range paths {
		if i > 0 && path == paths[i-1] {
			continue
		}
		entry := pathEntry{Path: path, Kind: "missing", Data: []int{}, Target: []int{}}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			result = append(result, entry)
			continue
		}
		if err != nil {
			return nil, err
		}
		switch mode := info.Mode(); {
		case mode&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return nil, err
			}
			entry.Kind = "symlink"
			entry.Target = numbers([]byte(target))
		case mode.IsRegular():
			fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return nil, err
			}
			file := os.NewFile(uintptr(fd), path)
			info, statErr := file.Stat()
			if statErr != nil {
				_ = file.Close()
				return nil, statErr
			}
			if !info.Mode().IsRegular() {
				_ = file.Close()
				return nil, fmt.Errorf("path is no longer a regular file: %s", path)
			}
			data, readErr := io.ReadAll(file)
			closeErr := file.Close()
			if err := errors.Join(readErr, closeErr); err != nil {
				return nil, err
			}
			entry.Kind = "file"
			entry.Data = numbers(data)
		case mode.IsDir():
			entry.Kind = "directory"
		default:
			return nil, fmt.Errorf("unsupported path kind: %s", path)
		}
		result = append(result, entry)
	}
	return result, nil
}

type processStatus struct {
	Kind   string `json:"kind"`
	Code   *int   `json:"code,omitempty"`
	Signal int    `json:"signal,omitempty"`
}
type observation struct {
	Stdout      []int         `json:"stdout"`
	Terminal    []int         `json:"terminal"`
	UnreadInput []int         `json:"unreadInput"`
	Status      processStatus `json:"status"`
	Restored    bool          `json:"termiosRestored"`
	Initial     []any         `json:"initialTermios"`
	Current     []any         `json:"currentTermios"`
}
type terminal struct {
	master, slave, stdout, inputRead, inputWrite *os.File
	inputReadFD, inputWriteFD, stdoutFD          int
	lifecycleMu                                  sync.Mutex
	closed                                       bool
	cmd                                          *exec.Cmd
	done                                         chan error
	exited                                       bool
	status                                       processStatus
	output                                       map[string][]byte
	cursors                                      map[string]int
	streams                                      map[int]string
	initial                                      []any
	pending, unread                              []byte
	pipe                                         bool
	deadline                                     time.Time
}

func termSettings(fd int) ([]any, error) {
	value, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	value.Lflag &^= unix.PENDIN
	// Python's tcgetattr exposes the Linux NCCS=32 control-character slots.
	cc := make([]int, 32)
	for i, v := range value.Cc {
		cc[i] = int(v)
	}
	outputSpeed := value.Cflag & unix.CBAUD
	inputSpeed := (value.Cflag & unix.CIBAUD) >> 16
	if inputSpeed == 0 {
		inputSpeed = outputSpeed
	}
	return []any{value.Iflag, value.Oflag, value.Cflag, value.Lflag, inputSpeed, outputSpeed, cc}, nil
}
func newTerminal(r request) (_ *terminal, err error) {
	budget, err := duration(r)
	if err != nil {
		return nil, err
	}
	rows, cols, err := geometry(r)
	if err != nil {
		return nil, err
	}
	var arguments []json.RawMessage
	if err := json.Unmarshal(r["argv"], &arguments); err != nil {
		return nil, err
	}
	if len(arguments) == 0 {
		return nil, fmt.Errorf("argv must be a nonempty array")
	}
	argv := make([]string, len(arguments))
	for i, raw := range arguments {
		if err := json.Unmarshal(raw, &argv[i]); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || strings.ContainsRune(argv[i], 0) {
			return nil, fmt.Errorf("argv entries must be NUL-free strings")
		}
	}
	if argv[0] == "" {
		return nil, fmt.Errorf("executable must not be empty")
	}
	t := &terminal{deadline: time.Now().Add(budget), status: processStatus{Kind: "running"}, output: map[string][]byte{"stdout": {}, "terminal": {}}, cursors: map[string]int{}, streams: map[int]string{}}
	defer func() {
		if err != nil {
			err = errors.Join(err, t.close())
		}
	}()
	if raw, ok := r["input"]; ok && string(raw) != "null" {
		t.pipe = true
		t.pending, err = byteData(raw)
		if err != nil {
			return nil, err
		}
	}
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	t.master = os.NewFile(uintptr(fd), "ptmx")
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		return nil, err
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		return nil, err
	}
	slaveFD, err := unix.Open(fmt.Sprintf("/dev/pts/%d", number), unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	t.slave = os.NewFile(uintptr(slaveFD), "pty slave")
	if err = unix.IoctlSetWinsize(slaveFD, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
		return nil, err
	}
	t.initial, err = termSettings(slaveFD)
	if err != nil {
		return nil, err
	}
	input := t.slave
	if t.pipe {
		t.inputRead, t.inputWrite, err = os.Pipe()
		if err != nil {
			return nil, err
		}
		t.inputReadFD = int(t.inputRead.Fd())
		t.inputWriteFD = int(t.inputWrite.Fd())
		input = t.inputRead
		if err = unix.SetNonblock(t.inputWriteFD, true); err != nil {
			return nil, err
		}
	}
	var outputWrite *os.File
	t.stdout, outputWrite, err = os.Pipe()
	if err != nil {
		return nil, err
	}
	t.stdoutFD = int(t.stdout.Fd())
	t.cmd = exec.Command(argv[0], argv[1:]...)
	t.cmd.Stdin = input
	t.cmd.Stdout = outputWrite
	t.cmd.Stderr = t.slave
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 2}
	err = t.cmd.Start()
	closeErr := outputWrite.Close()
	if err != nil {
		return nil, err
	}
	t.done = make(chan error, 1)
	go func() { t.done <- t.cmd.Wait() }()
	if closeErr != nil {
		return nil, closeErr
	}
	if err = unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	if err = unix.SetNonblock(t.stdoutFD, true); err != nil {
		return nil, err
	}
	t.streams[fd] = "terminal"
	t.streams[t.stdoutFD] = "stdout"
	if err = t.feed(); err != nil {
		return nil, err
	}
	return t, nil
}
func (t *terminal) remaining(until time.Time) (time.Duration, error) {
	deadline := t.deadline
	if !until.IsZero() && until.Before(deadline) {
		deadline = until
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, fmt.Errorf("terminal deadline expired")
	}
	return remaining, nil
}
func (t *terminal) pollExit() {
	if t.exited || t.done == nil {
		return
	}
	select {
	case <-t.done:
		t.exited = true
		code := t.cmd.ProcessState.ExitCode()
		if status, ok := t.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			t.status = processStatus{Kind: "signaled", Signal: int(status.Signal())}
		} else {
			t.status = processStatus{Kind: "exited", Code: &code}
		}
	default:
	}
}
func (t *terminal) feed() error {
	if t.inputWrite == nil {
		return nil
	}
	if len(t.pending) > 0 {
		n, err := unix.Write(t.inputWriteFD, t.pending)
		if n > 0 {
			t.pending = t.pending[n:]
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			return nil
		}
		if errors.Is(err, unix.EPIPE) {
			t.unread = append(t.unread, t.pending...)
			t.pending = nil
		} else if err != nil {
			return err
		}
	}
	if len(t.pending) == 0 {
		err := t.inputWrite.Close()
		t.inputWrite = nil
		return err
	}
	return nil
}
func (t *terminal) pump(wait time.Duration) (bool, error) {
	remaining, err := t.remaining(time.Time{})
	if err != nil {
		return false, err
	}
	if wait > remaining {
		wait = remaining
	}
	if err := t.feed(); err != nil {
		return false, err
	}
	fds := make([]unix.PollFd, 0, len(t.streams)+1)
	for fd := range t.streams {
		fds = append(fds, unix.PollFd{Fd: int32(fd), Events: unix.POLLIN})
	}
	if t.inputWrite != nil {
		fds = append(fds, unix.PollFd{Fd: int32(t.inputWriteFD), Events: unix.POLLOUT})
	}
	millis := int((wait + time.Millisecond - 1) / time.Millisecond)
	_, err = unix.Poll(fds, millis)
	if errors.Is(err, unix.EINTR) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ready := false
	buffer := make([]byte, 65536)
	for _, fd := range fds {
		if fd.Revents == 0 {
			continue
		}
		channel, ok := t.streams[int(fd.Fd)]
		if !ok {
			if err := t.feed(); err != nil {
				return false, err
			}
			continue
		}
		n, err := unix.Read(int(fd.Fd), buffer)
		if n > 0 {
			t.output[channel] = append(t.output[channel], buffer[:n]...)
			ready = true
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil && !errors.Is(err, unix.EIO) {
			return false, err
		}
		if n == 0 || errors.Is(err, unix.EIO) {
			delete(t.streams, int(fd.Fd))
		}
	}
	t.pollExit()
	return ready, nil
}
func (t *terminal) drain() error {
	for range 64 {
		ready, err := t.pump(0)
		if err != nil {
			return err
		}
		if !ready {
			break
		}
	}
	return nil
}
func (t *terminal) collectUnread() error {
	if !t.exited || t.inputRead == nil {
		return nil
	}
	if t.inputWrite != nil {
		if err := t.inputWrite.Close(); err != nil {
			return err
		}
		t.inputWrite = nil
	}
	if err := unix.SetNonblock(t.inputReadFD, true); err != nil {
		return err
	}
	buffer := make([]byte, 65536)
	for {
		n, err := unix.Read(t.inputReadFD, buffer)
		if n > 0 {
			t.unread = append(t.unread, buffer[:n]...)
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EAGAIN) {
			break
		}
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	t.unread = append(t.unread, t.pending...)
	t.pending = nil
	err := t.inputRead.Close()
	t.inputRead = nil
	return err
}
func (t *terminal) observe() (observation, error) {
	err := t.drain()
	t.pollExit()
	err = errors.Join(err, t.collectUnread())
	current, settingsErr := termSettings(int(t.slave.Fd()))
	err = errors.Join(err, settingsErr)
	return observation{Stdout: numbers(t.output["stdout"]), Terminal: numbers(t.output["terminal"]), UnreadInput: numbers(t.unread), Status: t.status, Restored: reflect.DeepEqual(t.initial, current), Initial: t.initial, Current: current}, err
}
func (t *terminal) perform(r request) (observation, error) {
	operation, err := text(r, "operation")
	if err != nil {
		return observation{}, err
	}
	switch operation {
	case "observe":
		return t.observe()
	case "expect":
		channel, err := text(r, "channel")
		if err != nil {
			return observation{}, err
		}
		if channel != "stdout" && channel != "terminal" {
			return observation{}, fmt.Errorf("channel must be stdout or terminal")
		}
		needle, err := byteData(r["data"])
		if err != nil {
			return observation{}, err
		}
		if len(needle) == 0 {
			return observation{}, fmt.Errorf("expected bytes must not be empty")
		}
		budget, err := duration(r)
		if err != nil {
			return observation{}, err
		}
		until := time.Now().Add(budget)
		for {
			if err := t.drain(); err != nil {
				return observation{}, fmt.Errorf("waiting for %s bytes %v: %w", channel, numbers(needle), err)
			}
			if found := bytes.Index(t.output[channel][t.cursors[channel]:], needle); found >= 0 {
				t.cursors[channel] += found + len(needle)
				return t.observe()
			}
			t.pollExit()
			if t.exited {
				return observation{}, fmt.Errorf("child exited before %s contained %v", channel, numbers(needle))
			}
			remaining, err := t.remaining(until)
			if err != nil {
				return observation{}, fmt.Errorf("waiting for %s bytes %v: %w", channel, numbers(needle), err)
			}
			if _, err := t.pump(min(50*time.Millisecond, remaining)); err != nil {
				return observation{}, err
			}
		}
	case "send":
		if t.pipe {
			return observation{}, fmt.Errorf("cannot send terminal input when stdin is a pipe")
		}
		data, err := byteData(r["data"])
		if err != nil {
			return observation{}, err
		}
		for len(data) > 0 {
			t.pollExit()
			if t.exited {
				return observation{}, fmt.Errorf("cannot send input after child exit")
			}
			if _, err := t.pump(0); err != nil {
				return observation{}, err
			}
			n, err := unix.Write(int(t.master.Fd()), data)
			if n > 0 {
				data = data[n:]
			}
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				if _, err := t.pump(10 * time.Millisecond); err != nil {
					return observation{}, err
				}
				continue
			}
			if err != nil {
				return observation{}, err
			}
		}
	case "resize":
		rows, cols, err := geometry(r)
		if err != nil {
			return observation{}, err
		}
		if err := unix.IoctlSetWinsize(int(t.slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
			return observation{}, err
		}
		if err := unix.Kill(-t.cmd.Process.Pid, unix.SIGWINCH); err != nil {
			return observation{}, err
		}
	case "signal":
		name, err := text(r, "name")
		if err != nil {
			return observation{}, err
		}
		sig, ok := map[string]unix.Signal{"SIGINT": unix.SIGINT, "SIGTERM": unix.SIGTERM, "SIGHUP": unix.SIGHUP, "SIGQUIT": unix.SIGQUIT, "SIGKILL": unix.SIGKILL, "SIGUSR1": unix.SIGUSR1, "SIGUSR2": unix.SIGUSR2}[name]
		if !ok {
			return observation{}, fmt.Errorf("unsupported signal")
		}
		if err := unix.Kill(-t.cmd.Process.Pid, sig); err != nil {
			return observation{}, err
		}
	case "wait":
		budget, err := duration(r)
		if err != nil {
			return observation{}, err
		}
		until := time.Now().Add(budget)
		for {
			t.pollExit()
			if t.exited {
				break
			}
			remaining, err := t.remaining(until)
			if err != nil {
				return observation{}, err
			}
			if _, err := t.pump(min(50*time.Millisecond, remaining)); err != nil {
				return observation{}, err
			}
		}
		for {
			if _, err := t.remaining(until); err != nil {
				return observation{}, err
			}
			ready, err := t.pump(0)
			if err != nil {
				return observation{}, err
			}
			if !ready {
				break
			}
		}
	default:
		return observation{}, fmt.Errorf("unknown terminal operation: %s", operation)
	}
	return t.observe()
}
func (t *terminal) killGroup() error {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	if t.closed || t.cmd == nil || t.cmd.Process == nil {
		return nil
	}
	err := unix.Kill(-t.cmd.Process.Pid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
func (t *terminal) close() error {
	t.lifecycleMu.Lock()
	if t.closed {
		t.lifecycleMu.Unlock()
		return nil
	}
	t.closed = true
	var cleanupErr error
	if t.cmd != nil && t.cmd.Process != nil {
		if err := unix.Kill(-t.cmd.Process.Pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
			cleanupErr = err
		}
	}
	t.lifecycleMu.Unlock()
	if t.done != nil && !t.exited {
		select {
		case <-t.done:
			t.exited = true
		case <-time.After(2 * time.Second):
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("child did not reap within cleanup budget"))
		}
	}
	for _, f := range []*os.File{t.master, t.slave, t.stdout, t.inputRead, t.inputWrite} {
		if f != nil {
			cleanupErr = errors.Join(cleanupErr, f.Close())
		}
	}
	t.master = nil
	t.slave = nil
	t.stdout = nil
	t.inputRead = nil
	t.inputWrite = nil
	return cleanupErr
}

func receive(connection io.Reader) (request, error) {
	// Decode one JSON object. The stream is newline framed and callers have deadlines.
	var r request
	if err := json.NewDecoder(connection).Decode(&r); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("request must be an object")
	}
	return r, nil
}
func requestSession(r request) reply {
	path, err := text(r, "path")
	if err != nil {
		return failure(err, nil)
	}
	connection, err := net.DialTimeout("unix", path, 65*time.Second)
	if err != nil {
		operation, _ := text(r, "operation")
		if operation == "close" && errors.Is(err, os.ErrNotExist) {
			return success(nil)
		}
		return failure(err, nil)
	}
	defer func() { _ = connection.Close() }()
	if err := connection.SetDeadline(time.Now().Add(65 * time.Second)); err != nil {
		return failure(err, nil)
	}
	if err := json.NewEncoder(connection).Encode(r); err != nil {
		return failure(err, nil)
	}
	var result reply
	if err := json.NewDecoder(connection).Decode(&result); err != nil {
		return failure(err, nil)
	}
	return result
}
func start(r request) reply {
	if _, _, err := geometry(r); err != nil {
		return failure(err, nil)
	}
	budget, err := duration(r)
	if err != nil {
		return failure(err, nil)
	}
	directory, err := os.MkdirTemp("/tmp", "caseyard-pty-")
	if err != nil {
		return failure(err, nil)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(directory)
		}
	}()
	read, write, err := os.Pipe()
	if err != nil {
		return failure(err, nil)
	}
	defer func() { _ = read.Close() }()
	executable, err := os.Executable()
	if err != nil {
		_ = write.Close()
		return failure(err, nil)
	}
	childRequest := make(request, len(r)+1)
	for k, v := range r {
		childRequest[k] = v
	}
	encodedDirectory, err := json.Marshal(directory)
	if err != nil {
		_ = write.Close()
		return failure(err, nil)
	}
	childRequest["directory"] = encodedDirectory
	encoded, err := json.Marshal(childRequest)
	if err != nil {
		_ = write.Close()
		return failure(err, nil)
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		_ = write.Close()
		return failure(err, nil)
	}
	defer func() { _ = null.Close() }()
	child := exec.Command(executable, "serve", string(encoded))
	child.Stdin = null
	child.Stdout = null
	child.Stderr = null
	child.ExtraFiles = []*os.File{write}
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = child.Start()
	writeErr := write.Close()
	if err != nil {
		return failure(err, nil)
	}
	// The detached server owns cleanup even if its readiness response is lost.
	// Never kill that owner, which would bypass its application watchdog.
	keep = true
	defer func() { _ = child.Process.Release() }()
	if err := read.SetReadDeadline(time.Now().Add(budget)); err != nil {
		return failure(err, nil)
	}
	var result reply
	err = json.NewDecoder(read).Decode(&result)
	err = errors.Join(err, writeErr)
	if err != nil {
		return failure(err, nil)
	}
	return result
}
func serve(r request) reply {
	ready := os.NewFile(3, "ready")
	if ready == nil {
		return failure(fmt.Errorf("missing readiness descriptor"), nil)
	}
	defer func() { _ = ready.Close() }()
	directory, err := text(r, "directory")
	if err != nil {
		result := failure(err, nil)
		_ = json.NewEncoder(ready).Encode(result)
		return result
	}
	if filepath.Dir(directory) != "/tmp" || !strings.HasPrefix(filepath.Base(directory), "caseyard-pty-") {
		result := failure(fmt.Errorf("invalid owned session directory"), nil)
		_ = json.NewEncoder(ready).Encode(result)
		return result
	}
	info, statErr := os.Lstat(directory)
	if statErr != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		result := failure(fmt.Errorf("invalid private session directory: %v", statErr), nil)
		_ = json.NewEncoder(ready).Encode(result)
		return result
	}
	defer func() { _ = os.RemoveAll(directory) }()
	t, err := newTerminal(r)
	if err != nil {
		result := failure(err, nil)
		_ = json.NewEncoder(ready).Encode(result)
		return result
	}
	defer func() { _ = t.close() }()
	path := filepath.Join(directory, "socket")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		result := failure(err, nil)
		_ = json.NewEncoder(ready).Encode(result)
		return result
	}
	listener.SetUnlinkOnClose(false)
	defer func() { _ = listener.Close() }()
	// Keep application cleanup independent of blocked requests or output writes.
	watchdogDone := make(chan struct{})
	watchdog := time.AfterFunc(time.Until(t.deadline), func() {
		defer close(watchdogDone)
		_ = t.killGroup()
		_ = listener.Close()
	})
	defer func() {
		if !watchdog.Stop() {
			<-watchdogDone
		}
	}()
	if err := json.NewEncoder(ready).Encode(success(map[string]string{"path": path})); err != nil {
		return failure(err, nil)
	}
	_ = ready.Close()
	for {
		remaining, err := t.remaining(time.Time{})
		if err != nil {
			return failure(err, nil)
		}
		if err := listener.SetDeadline(time.Now().Add(min(50*time.Millisecond, remaining))); err != nil {
			return failure(err, nil)
		}
		connection, err := listener.AcceptUnix()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if err := t.drain(); err != nil {
					return failure(err, nil)
				}
				continue
			}
			return failure(err, nil)
		}
		remaining, err = t.remaining(time.Time{})
		if err != nil {
			_ = connection.Close()
			return failure(err, nil)
		}
		if err := connection.SetDeadline(time.Now().Add(min(2*time.Second, remaining))); err != nil {
			_ = connection.Close()
			return failure(err, nil)
		}
		operation, err := receive(connection)
		var result reply
		stopping := false
		if err != nil {
			result = failure(err, nil)
		} else if op, _ := text(operation, "operation"); op == "close" {
			cleanupErr := t.close()
			_ = listener.Close()
			cleanupErr = errors.Join(cleanupErr, os.RemoveAll(directory))
			if cleanupErr != nil {
				result = failure(cleanupErr, nil)
			} else {
				result = success(nil)
			}
			stopping = true
		} else {
			value, err := t.perform(operation)
			if err != nil {
				observed, _ := t.observe()
				result = failure(err, observed)
			} else {
				result = success(value)
			}
		}
		// Operations may wait longer than the short request-read deadline.
		writeDeadline := time.Now().Add(2 * time.Second)
		if end := t.deadline.Add(100 * time.Millisecond); end.Before(writeDeadline) {
			writeDeadline = end
		}
		_ = connection.SetWriteDeadline(writeDeadline)
		_ = json.NewEncoder(connection).Encode(result)
		_ = connection.Close()
		if stopping {
			return success(nil)
		}
	}
}
