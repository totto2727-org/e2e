package driver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLostReadinessResponseCleansOwnedApplication(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "caseyard-pty-lost-ready-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	r := req(t, map[string]any{"directory": directory, "argv": []string{"sh", "-c", "exec sleep 10"}, "timeout": 0.3})
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "serve", string(encoded))
	child.ExtraFiles = []*os.File{write}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = child.Process.Kill()
		t.Fatal("lost readiness left server alive")
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost readiness leaked owned directory: %v", err)
	}
}

func TestWatchdogKillsWhileControlWriteBlocked(t *testing.T) {
	result := start(req(t, map[string]any{"argv": []string{"sh", "-c", "echo $$; read begin; head -c 2097152 /dev/zero >&2; exec sleep 10"}, "timeout": 0.7}))
	if !result.OK {
		t.Fatalf("start: %#v", result)
	}
	path := result.Value.(map[string]any)["path"].(string)
	t.Cleanup(func() { requestSession(req(t, map[string]any{"path": path, "operation": "close"})) })
	result = requestSession(req(t, map[string]any{"path": path, "operation": "expect", "channel": "stdout", "data": []int{10}}))
	if !result.OK {
		t.Fatalf("expect: %#v", result)
	}
	var pidText strings.Builder
	for _, v := range result.Value.(map[string]any)["stdout"].([]any) {
		pidText.WriteByte(byte(v.(float64)))
	}
	var pid int
	if _, err := fmt.Sscanf(pidText.String(), "%d", &pid); err != nil {
		t.Fatal(err)
	}
	result = requestSession(req(t, map[string]any{"path": path, "operation": "send", "data": numbers([]byte("go\n"))}))
	if !result.OK {
		t.Fatalf("send: %#v", result)
	}
	// Wait for enough bytes to exceed the Unix socket's send buffer, not readiness.
	time.Sleep(100 * time.Millisecond)
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := json.NewEncoder(connection).Encode(map[string]any{"operation": "observe"}); err != nil {
		t.Fatal(err)
	}
	// Do not read the response. Application lifetime must not depend on this client.
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(filepath.Dir(path)); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stalled control writer prolonged watchdog cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := unix.Kill(pid, 0); !errors.Is(err, unix.ESRCH) {
		t.Fatalf("stalled response left application alive: %v", err)
	}
}

func TestDetachedServerLifecycleBoundary(t *testing.T) {
	result := start(req(t, map[string]any{"argv": []string{"sh", "-c", "echo $PPID; exec sleep 10"}, "timeout": 3}))
	if !result.OK {
		t.Fatalf("start: %#v", result)
	}
	path := result.Value.(map[string]any)["path"].(string)
	t.Cleanup(func() { requestSession(req(t, map[string]any{"path": path, "operation": "close"})) })
	result = requestSession(req(t, map[string]any{"path": path, "operation": "expect", "channel": "stdout", "data": []int{10}}))
	if !result.OK {
		t.Fatalf("expect: %#v", result)
	}
	var pidText strings.Builder
	for _, v := range result.Value.(map[string]any)["stdout"].([]any) {
		pidText.WriteByte(byte(v.(float64)))
	}
	var pid int
	if _, err := fmt.Sscanf(pidText.String(), "%d", &pid); err != nil {
		t.Fatal(err)
	}
	result = requestSession(req(t, map[string]any{"path": path, "operation": "close"}))
	if !result.OK {
		t.Fatalf("close: %#v", result)
	}
	// Detached server reaping is the container init's responsibility, not application cleanup.
	deadline := time.Now().Add(time.Second)
	for {
		status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if errors.Is(err, os.ErrNotExist) {
			t.Log("detached server reaped")
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		zombie := false
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "State:") && strings.Contains(line, "Z") {
				t.Logf("detached server stopped: %s; orphan reaping requires container init or teardown", line)
				zombie = true
			}
		}
		if zombie {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached server still live after close")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
