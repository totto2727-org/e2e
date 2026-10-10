package cli

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/client"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

func TestExecCommand_preserves_options_output_and_exit_code(t *testing.T) {
	connection, peer := net.Pipe()
	t.Cleanup(func() { closeTestConnection(t, connection) })
	t.Cleanup(func() { closeTestConnection(t, peer) })
	written := make(chan error, 1)
	go func() {
		var err error
		for _, frame := range []struct {
			stream byte
			text   string
		}{{2, "diagnostic\n"}, {1, "result\n"}} {
			var header [8]byte
			header[0] = frame.stream
			binary.BigEndian.PutUint32(header[4:], uint32(len(frame.text)))
			if _, err = peer.Write(append(header[:], []byte(frame.text)...)); err != nil {
				break
			}
		}
		written <- errors.Join(err, peer.Close())
	}()
	executor := &fakeExecutor{connection: connection, exitCode: 7}
	exitCode, output, err := execCommand(t.Context(), executor, "container", []string{"example", "arg with spaces"},
		tcexec.Multiplexed(), tcexec.WithWorkingDir("/project"), tcexec.WithEnv([]string{"HOME=/home"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	if exitCode != 7 || string(content) != "result\ndiagnostic\n" {
		t.Fatalf("exit=%d output=%q", exitCode, content)
	}
	if strings.Join(executor.options.Cmd, "|") != "example|arg with spaces" || executor.options.WorkingDir != "/project" || strings.Join(executor.options.Env, "|") != "HOME=/home" {
		t.Fatalf("options=%+v", executor.options)
	}
}

func TestExecCommand_deadline_closes_open_output(t *testing.T) {
	connection, peer := net.Pipe()
	t.Cleanup(func() { closeTestConnection(t, connection) })
	t.Cleanup(func() { closeTestConnection(t, peer) })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := execCommand(ctx, &fakeExecutor{connection: connection}, "container", []string{"sleep", "infinity"}, tcexec.Multiplexed())
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not return after its deadline")
	}
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("attach connection remains open: %v", err)
	}
}

func TestExecCommand_cancellation_interrupts_exit_polling(t *testing.T) {
	connection, peer := net.Pipe()
	t.Cleanup(func() { closeTestConnection(t, connection) })
	closeTestConnection(t, peer)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	executor := &fakeExecutor{connection: connection, running: true, onInspect: cancel}
	_, _, err := execCommand(ctx, executor, "container", []string{"example"}, tcexec.Multiplexed())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestExecCommand_preserves_create_failure(t *testing.T) {
	cause := errors.New("create rejected")
	_, _, err := execCommand(t.Context(), &fakeExecutor{createErr: cause}, "container", []string{"example"})
	if !errors.Is(err, cause) {
		t.Fatalf("error=%v", err)
	}
}

// This runs the real Moby client against a controlled HTTP boundary. It is not
// a substitute for the Docker-backed example, but protects both upgrade and
// streaming cancellation paths without requiring Docker for package tests.
func TestExecClient_cancels_stalled_upgrade_and_stream(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade=%t", upgrade), func(t *testing.T) {
			started := make(chan struct{})
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				switch {
				case strings.HasSuffix(request.URL.Path, "/containers/container/exec"):
					w.Header().Set("Content-Type", "application/json")
					if _, err := io.WriteString(w, `{"Id":"exec"}`); err != nil {
						t.Error(err)
					}
				case strings.HasSuffix(request.URL.Path, "/exec/exec/start"):
					if _, err := io.Copy(io.Discard, request.Body); err != nil {
						t.Error(err)
						return
					}
					hijacker, ok := w.(http.Hijacker)
					if !ok {
						t.Error("server does not support hijack")
						return
					}
					connection, buffer, err := hijacker.Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer closeTestConnection(t, connection)
					if upgrade {
						if _, err := buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"); err != nil {
							t.Error(err)
							return
						}
						if err := buffer.Flush(); err != nil {
							t.Error(err)
							return
						}
					}
					close(started)
					if _, err := connection.Read(make([]byte, 1)); !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
						t.Errorf("connection close: %v", err)
					}
					close(closed)
				default:
					t.Errorf("unexpected request %s", request.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			base, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithAPIVersion("1.54"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := base.Close(); err != nil {
					t.Error(err)
				}
			})
			executor, err := newExecClient(base)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := executor.Close(); err != nil {
					t.Error(err)
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, _, err := execCommand(ctx, executor, "container", []string{"sleep", "infinity"}, tcexec.Multiplexed())
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("exec did not reach attach")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("exec remained blocked after cancellation")
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("server connection remained open")
			}
		})
	}
}

type fakeExecutor struct {
	connection net.Conn
	options    client.ExecCreateOptions
	exitCode   int
	running    bool
	createErr  error
	onInspect  func()
}

func (f *fakeExecutor) ExecCreate(_ context.Context, _ string, options client.ExecCreateOptions) (client.ExecCreateResult, error) {
	f.options = options
	return client.ExecCreateResult{ID: "exec"}, f.createErr
}

func (f *fakeExecutor) ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
	return client.ExecAttachResult{HijackedResponse: client.HijackedResponse{Conn: f.connection, Reader: bufio.NewReader(f.connection)}}, nil
}

func (f *fakeExecutor) ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
	if f.onInspect != nil {
		f.onInspect()
	}
	return client.ExecInspectResult{Running: f.running, ExitCode: f.exitCode}, nil
}

func closeTestConnection(t *testing.T, connection net.Conn) {
	t.Helper()
	if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
}
