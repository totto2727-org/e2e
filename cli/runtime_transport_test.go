package cli

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

func TestExecClient_preserves_TLS_transport(t *testing.T) {
	handler, _, _ := execTransportHandler(t, false)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	base, err := client.New(
		client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "https://")),
		client.WithAPIVersion("1.54"),
		client.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTransportExec(t, base)
}

func TestExecClient_preserves_unix_transport(t *testing.T) {
	for _, cancelStream := range []bool{false, true} {
		name := "success"
		if cancelStream {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			// A short directory avoids macOS's Unix socket path-length limit.
			directory, err := os.MkdirTemp("", "e2e-unix-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(directory); err != nil {
					t.Error(err)
				}
			})
			socket := filepath.Join(directory, "docker.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			handler, started, closed := execTransportHandler(t, cancelStream)
			server := httptest.NewUnstartedServer(handler)
			if err := server.Listener.Close(); err != nil {
				t.Fatal(err)
			}
			server.Listener = listener
			server.Start()
			t.Cleanup(server.Close)
			base, err := client.New(client.WithHost("unix://"+socket), client.WithAPIVersion("1.54"))
			if err != nil {
				t.Fatal(err)
			}
			if !cancelStream {
				assertTransportExec(t, base)
				return
			}
			executor := transportExecutor(t, base)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, _, err := execCommand(ctx, executor, "container", []string{"example"}, tcexec.Multiplexed())
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("Unix exec did not reach attach")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Unix exec remained blocked after cancellation")
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("Unix attach connection remained open")
			}
		})
	}
}

func transportExecutor(t *testing.T, base *client.Client) *client.Client {
	t.Helper()
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
	return executor
}

func assertTransportExec(t *testing.T, base *client.Client) {
	t.Helper()
	executor := transportExecutor(t, base)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	exitCode, output, err := execCommand(ctx, executor, "container", []string{"example"}, tcexec.Multiplexed())
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	if exitCode != 7 || string(content) != "transport output\n" {
		t.Fatalf("exit=%d output=%q", exitCode, content)
	}
}

func execTransportHandler(t *testing.T, cancelStream bool) (http.Handler, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	closed := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/containers/container/exec"):
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"Id":"exec"}`); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(request.URL.Path, "/exec/exec/json"):
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"ID":"exec","Running":false,"ExitCode":7}`); err != nil {
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
			defer close(closed)
			if _, err := buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"); err != nil {
				t.Error(err)
				return
			}
			if err := buffer.Flush(); err != nil {
				t.Error(err)
				return
			}
			close(started)
			if cancelStream {
				if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					t.Error(err)
					return
				}
				if _, err := connection.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
					t.Errorf("canceled attach connection: %v", err)
				}
				return
			}
			output := []byte("transport output\n")
			var header [8]byte
			header[0] = 1
			binary.BigEndian.PutUint32(header[4:], uint32(len(output)))
			if _, err := connection.Write(append(header[:], output...)); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected request %s", request.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return handler, started, closed
}
