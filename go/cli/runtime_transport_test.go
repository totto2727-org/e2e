package cli

import (
	"context"
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

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

func TestExecClient_negotiates_before_cloning_version(t *testing.T) {
	for _, fixedVersion := range []string{"", "1.47"} {
		name := "automatic"
		wantVersion := "1.48"
		if fixedVersion != "" {
			name = "explicit"
			wantVersion = fixedVersion
		}
		t.Run(name, func(t *testing.T) {
			handler, _, _ := execTransportHandler(t, false)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/_ping" {
					w.Header().Set("API-Version", "1.48")
					w.WriteHeader(http.StatusOK)
					return
				}
				if !strings.HasPrefix(request.URL.Path, "/v"+wantVersion+"/") {
					t.Errorf("request=%s want API %s", request.URL.Path, wantVersion)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				handler.ServeHTTP(w, request)
			}))
			t.Cleanup(server.Close)
			options := []client.Opt{client.WithHost("tcp://" + strings.TrimPrefix(server.URL, "http://"))}
			if fixedVersion != "" {
				options = append(options, client.WithAPIVersion(fixedVersion))
			}
			base, err := client.New(options...)
			if err != nil {
				t.Fatal(err)
			}
			// No base request occurs before newExecClient: this was the CI failure.
			assertTransportExec(t, base)
			if base.ClientVersion() != wantVersion {
				t.Fatalf("version=%s want=%s", base.ClientVersion(), wantVersion)
			}
		})
	}
}

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
				_, err := runCommand(ctx, executor, "container", Command{Args: []string{"example"}})
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
	executor, err := newExecClient(t.Context(), base)
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
	result, err := runCommand(ctx, executor, "container", Command{Args: []string{"example"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{ExitCode: 7, Stdout: "transport output\n", Stderr: "transport diagnostic\n"}); result != want {
		t.Fatalf("result=%+v want=%+v", result, want)
	}
}

func execTransportHandler(t *testing.T, cancelStream bool) (http.Handler, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	closed := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/_ping":
			w.Header().Set("API-Version", "1.54")
			w.WriteHeader(http.StatusOK)
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
			if err := writeFrame(connection, stdcopy.Stderr, "transport diagnostic\n"); err != nil {
				t.Error(err)
				return
			}
			if err := writeFrame(connection, stdcopy.Stdout, "transport output\n"); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected request %s", request.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return handler, started, closed
}
