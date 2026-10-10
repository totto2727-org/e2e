package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

const (
	maxParallelScenarios     = 2
	imageLeaseCleanupTimeout = 30 * time.Second
	containerCleanupTimeout  = 30 * time.Second
	scenarioTimeout          = time.Minute
)

type Case struct {
	Name string
	Run  func(*testing.T, *Environment)
}

func Run(t *testing.T, image string, cases []Case) {
	t.Helper()
	imageID, releaseImage, err := retainLocalImage(t.Context(), image)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if releaseErr := releaseImage(); releaseErr != nil {
			t.Error(releaseErr)
		}
	})
	slots := make(chan struct{}, maxParallelScenarios)
	for _, testCase := range cases {
		t.Run(testCase.Name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel, err := acquireScenario(t.Context(), slots, scenarioTimeout)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			t.Log("started")
			t.Cleanup(func() { <-slots })
			t.Cleanup(func() { t.Logf("completed pass=%t", !t.Failed()) })
			container, err := testcontainers.Run(ctx, imageID)
			t.Cleanup(func() {
				if container != nil {
					if err := cleanupContainer(container, containerCleanupTimeout); err != nil {
						t.Error(err)
					}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("container=%s", container.GetContainerID())
			base, err := testcontainers.NewDockerClientWithOpts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := base.Close(); err != nil {
					t.Error(err)
				}
			})
			executor, err := newExecClient(base.Client)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := executor.Close(); err != nil {
					t.Error(err)
				}
			})
			testCase.Run(t, &Environment{ctx: ctx, container: &dockerRuntime{DockerContainer: container, executor: executor}})
		})
	}
}

// The scenario budget starts only after a slot is acquired. Queue cancellation
// follows the parent test, not another scenario's execution budget.
func acquireScenario(parent context.Context, slots chan struct{}, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	select {
	case slots <- struct{}{}:
	case <-parent.Done():
		return nil, nil, parent.Err()
	}
	if err := parent.Err(); err != nil {
		<-slots
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, cancel, nil
}

type terminableContainer interface {
	Terminate(context.Context, ...testcontainers.TerminateOption) error
}

func cleanupContainer(container terminableContainer, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := container.Terminate(ctx); err != nil {
		return fmt.Errorf("terminate scenario container: %w", err)
	}
	return nil
}

func retainLocalImage(ctx context.Context, image string) (string, func() error, error) {
	dockerClient, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return "", nil, err
	}
	inspected, err := dockerClient.ImageInspect(ctx, image)
	if err != nil {
		return "", nil, errors.Join(err, dockerClient.Close())
	}
	lease, err := dockerClient.ContainerCreate(ctx, client.ContainerCreateOptions{Image: inspected.ID})
	if err != nil {
		return "", nil, errors.Join(err, dockerClient.Close())
	}
	release := func() error {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), imageLeaseCleanupTimeout)
		defer cleanupCancel()
		_, removeErr := dockerClient.ContainerRemove(
			cleanupCtx,
			lease.ID,
			client.ContainerRemoveOptions{},
		)
		return errors.Join(removeErr, dockerClient.Close())
	}
	return inspected.ID, release, nil
}
