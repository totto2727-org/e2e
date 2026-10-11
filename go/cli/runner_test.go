package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

func TestValidateCases(t *testing.T) {
	action := func(*testing.T, *Environment) {
		t.Fatal("validation must not invoke a case callback")
	}
	for _, test := range []struct {
		name      string
		cases     []Case
		wantError string
	}{
		{name: "nil cases"},
		{name: "empty cases", cases: []Case{}},
		{name: "one case", cases: []Case{{Name: "first", Run: action}}},
		{name: "distinct names", cases: []Case{{Name: "first", Run: action}, {Name: "second", Run: action}}},
		{name: "literal names", cases: []Case{{Name: "First", Run: action}, {Name: "first", Run: action}, {Name: " first ", Run: action}}},
		{name: "empty name", cases: []Case{{Run: action}}, wantError: "case 0: name must not be empty"},
		{name: "later empty name", cases: []Case{{Name: "first", Run: action}, {Run: action}}, wantError: "case 1: name must not be empty"},
		{name: "duplicate name", cases: []Case{{Name: "first", Run: action}, {Name: "first", Run: action}}, wantError: `duplicate name "first"`},
		{name: "nonadjacent duplicate", cases: []Case{{Name: "first", Run: action}, {Name: "second", Run: action}, {Name: "first", Run: action}}, wantError: `case 2: duplicate name "first"`},
		{name: "nil callback", cases: []Case{{Name: "first"}}, wantError: `case 0 "first": callback must not be nil`},
		{name: "later nil callback", cases: []Case{{Name: "first", Run: action}, {Name: "second"}}, wantError: `case 1 "second": callback must not be nil`},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateCases(test.cases)
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error=%v want containing %q", err, test.wantError)
			}
		})
	}
}

func TestAcquireScenario_queue_does_not_consume_execution_budget(t *testing.T) {
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	const budget = 50 * time.Millisecond
	result := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		close(ready)
		ctx, cancel, err := acquireScenario(t.Context(), slots, budget)
		if err != nil {
			result <- err
			return
		}
		defer cancel()
		defer func() { <-slots }()
		deadline, ok := ctx.Deadline()
		if !ok || ctx.Err() != nil || time.Until(deadline) < budget/2 {
			result <- errors.New("queued case did not receive a fresh execution budget")
			return
		}
		result <- nil
	}()
	<-ready
	// Keep the slot occupied longer than the entire execution budget.
	select {
	case err := <-result:
		t.Fatalf("case stopped while waiting for a slot: %v", err)
	case <-time.After(3 * budget):
	}
	<-slots
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued case did not acquire the released slot")
	}
}

func TestAcquireScenario_parent_cancellation_releases_waiter(t *testing.T) {
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := acquireScenario(ctx, slots, time.Minute)
	if !errors.Is(err, context.Canceled) || len(slots) != 1 {
		t.Fatalf("error=%v slots=%d", err, len(slots))
	}
}

func TestAcquireScenario_canceled_parent_does_not_hold_available_slot(t *testing.T) {
	slots := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := acquireScenario(ctx, slots, time.Minute)
	if !errors.Is(err, context.Canceled) || len(slots) != 0 {
		t.Fatalf("error=%v slots=%d", err, len(slots))
	}
}

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
