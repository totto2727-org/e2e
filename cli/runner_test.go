package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

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

func (c *fakeTerminatingContainer) Terminate(ctx context.Context, options ...testcontainers.TerminateOption) error {
	return c.terminate(ctx)
}
