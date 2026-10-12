package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

func TestCleanupContainer_uses_fresh_bounded_context(t *testing.T) {
	container := &fakeTerminatingContainer{terminate: func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Fatal("cleanup context is already canceled")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 50*time.Millisecond {
			t.Fatal("cleanup has no bounded deadline")
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	if err := cleanupContainer(container, 50*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}

func TestCleanupContainer_preserves_termination_failure(t *testing.T) {
	cause := errors.New("remove rejected")
	container := &fakeTerminatingContainer{terminate: func(context.Context) error { return cause }}
	if err := cleanupContainer(container, time.Second); !errors.Is(err, cause) {
		t.Fatalf("error=%v", err)
	}
}

type fakeTerminatingContainer struct {
	terminate func(context.Context) error
}

func TestRetainImageLease_each_invocation_owns_its_resolved_image(t *testing.T) {
	var mu sync.Mutex
	leases := make(map[string]container.Config)
	resolved := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/images/image/json"):
			resolved++
			if err := json.NewEncoder(w).Encode(map[string]string{"Id": "sha256:image" + strconv.Itoa(resolved)}); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(request.URL.Path, "/containers/create"):
			name := request.URL.Query().Get("name")
			var config container.Config
			if err := json.NewDecoder(request.Body).Decode(&config); err != nil {
				t.Error(err)
				return
			}
			if _, exists := leases[name]; exists || name == "" || config.Labels[imageLeaseOwnerLabel] == "" {
				t.Errorf("lease name=%q reused or missing ownership", name)
			}
			leases[name] = config
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(map[string]string{"Id": name}); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(request.URL.Path, "/json"):
			name := path.Base(strings.TrimSuffix(request.URL.Path, "/json"))
			config, exists := leases[name]
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"Id": name, "Config": config}); err != nil {
				t.Error(err)
			}
		case request.Method == http.MethodDelete:
			name := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
			if _, exists := leases[name]; !exists {
				t.Errorf("remove unowned lease %q", name)
			}
			delete(leases, name)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	var releases []func() error
	for index := range 2 {
		dockerClient, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithAPIVersion("1.54"))
		if err != nil {
			t.Fatal(err)
		}
		imageID, release, err := retainImageLease(t.Context(), dockerClient, "image")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := release(); err != nil {
				t.Error(err)
			}
		})
		if want := "sha256:image" + strconv.Itoa(index+1); imageID != want {
			t.Fatalf("image=%q want=%q", imageID, want)
		}
		releases = append(releases, release)
	}
	mu.Lock()
	if resolved != 2 || len(leases) != 2 {
		t.Errorf("resolutions=%d leases=%d want=2 each", resolved, len(leases))
	}
	owners := make(map[string]struct{})
	for _, config := range leases {
		owners[config.Labels[imageLeaseOwnerLabel]] = struct{}{}
	}
	if len(owners) != 2 {
		t.Error("invocations shared an ownership token")
	}
	mu.Unlock()
	if err := releases[0](); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(leases) != 1 {
		t.Fatalf("remaining leases=%d want=1", len(leases))
	}
	for _, config := range leases {
		if config.Image != "sha256:image2" {
			t.Fatalf("remaining leased image=%q want second immutable ID", config.Image)
		}
	}
}

func TestRetainImageLease_owned_cleanup(t *testing.T) {
	for _, test := range []struct {
		name         string
		loseResponse bool
		foreign      bool
		absent       bool
		removeStatus int
		wantError    string
	}{
		{name: "release owned lease"},
		{name: "response lost after creation", loseResponse: true, wantError: "create image lease"},
		{name: "foreign owner refused", foreign: true, wantError: "ownership label mismatch"},
		{name: "absent lease succeeds", absent: true},
		{name: "lease disappears before remove", removeStatus: http.StatusNotFound},
		{name: "response lost and cleanup failed", loseResponse: true, removeStatus: http.StatusInternalServerError, wantError: "remove image lease"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var mu sync.Mutex
			var leaseName, owner, removedID string
			inspections := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(request.URL.Path, "/images/image/json"):
					if _, err := io.WriteString(w, `{"Id":"sha256:resolved"}`); err != nil {
						t.Error(err)
					}
				case strings.HasSuffix(request.URL.Path, "/containers/create"):
					leaseName = request.URL.Query().Get("name")
					var config container.Config
					if err := json.NewDecoder(request.Body).Decode(&config); err != nil {
						t.Error(err)
						return
					}
					owner = config.Labels[imageLeaseOwnerLabel]
					if leaseName == "" || owner == "" || config.Image != "sha256:resolved" {
						t.Errorf("name=%q owner=%q image=%q", leaseName, owner, config.Image)
					}
					if test.loseResponse {
						hijacker, ok := w.(http.Hijacker)
						if !ok {
							t.Error("server does not support hijack")
							return
						}
						connection, _, err := hijacker.Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						cancel()
						closeTestConnection(t, connection)
						return
					}
					w.WriteHeader(http.StatusCreated)
					if _, err := io.WriteString(w, `{"Id":"create-response-id"}`); err != nil {
						t.Error(err)
					}
				case strings.HasSuffix(request.URL.Path, "/containers/"+leaseName+"/json"):
					inspections++
					if test.absent {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					label := owner
					if test.foreign {
						label = "another-owner"
					}
					if err := json.NewEncoder(w).Encode(map[string]any{
						"Id":     "immutable-inspected-id",
						"Config": container.Config{Labels: map[string]string{imageLeaseOwnerLabel: label}},
					}); err != nil {
						t.Error(err)
					}
				case request.Method == http.MethodDelete:
					removedID = request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
					if test.removeStatus != 0 {
						w.WriteHeader(test.removeStatus)
						if _, err := io.WriteString(w, `{"message":"remove rejected"}`); err != nil {
							t.Error(err)
						}
						return
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			dockerClient, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithAPIVersion("1.54"))
			if err != nil {
				t.Fatal(err)
			}
			imageID, release, err := retainImageLease(ctx, dockerClient, "image")
			if !test.loseResponse {
				if err != nil || imageID != "sha256:resolved" || release == nil {
					t.Fatalf("image=%q release_nil=%t err=%v", imageID, release == nil, err)
				}
				cancel()
				err = release()
			} else if imageID != "" || release != nil {
				t.Fatalf("failed create returned image=%q release_nil=%t", imageID, release == nil)
			}
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("err=%v want containing %q", err, test.wantError)
			}
			if test.loseResponse && (err == nil || !strings.Contains(err.Error(), "create image lease")) {
				t.Fatalf("err=%v want primary creation error", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if inspections != 1 {
				t.Fatalf("lease inspections=%d want=1", inspections)
			}
			if test.foreign || test.absent {
				if removedID != "" {
					t.Fatalf("removed unowned or absent lease ID=%q", removedID)
				}
			} else if removedID != "immutable-inspected-id" {
				t.Fatalf("removed ID=%q want inspected immutable ID", removedID)
			}
		})
	}
}

func (c *fakeTerminatingContainer) Terminate(ctx context.Context, options ...testcontainers.TerminateOption) error {
	return c.terminate(ctx)
}
