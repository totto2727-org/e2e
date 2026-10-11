package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

const (
	maxParallelScenarios     = 2
	imageLeaseCleanupTimeout = 30 * time.Second
	containerCleanupTimeout  = 30 * time.Second
	scenarioTimeout          = time.Minute
	imageLeaseOwnerLabel     = "io.github.totto2727-org.e2e.image-lease-owner"
)

type Case struct {
	Name string
	Run  func(*testing.T, *Environment)
}

func Run(t *testing.T, image string, cases []Case) {
	t.Helper()
	if err := validateCases(cases); err != nil {
		t.Fatal(err)
	}
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
			executor, err := newExecClient(ctx, base.Client)
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

func validateCases(cases []Case) error {
	names := make(map[string]struct{}, len(cases))
	for index, testCase := range cases {
		if testCase.Name == "" {
			return fmt.Errorf("case %d: name must not be empty", index)
		}
		if _, exists := names[testCase.Name]; exists {
			return fmt.Errorf("case %d: duplicate name %q", index, testCase.Name)
		}
		if testCase.Run == nil {
			return fmt.Errorf("case %d %q: callback must not be nil", index, testCase.Name)
		}
		names[testCase.Name] = struct{}{}
	}
	return nil
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
	return retainImageLease(ctx, dockerClient.Client, image)
}

func retainImageLease(ctx context.Context, dockerClient *client.Client, image string) (string, func() error, error) {
	inspected, err := dockerClient.ImageInspect(ctx, image)
	if err != nil {
		return "", nil, errors.Join(err, dockerClient.Close())
	}
	name := "e2e-image-lease-" + rand.Text()
	owner := rand.Text()
	release := func() error {
		return errors.Join(cleanupImageLease(dockerClient, name, owner), dockerClient.Close())
	}
	_, err = dockerClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image:  inspected.ID,
			Labels: map[string]string{imageLeaseOwnerLabel: owner},
		},
	})
	if err != nil {
		return "", nil, errors.Join(fmt.Errorf("create image lease: %w", err), release())
	}
	return inspected.ID, release, nil
}

func cleanupImageLease(dockerClient *client.Client, name, owner string) error {
	ctx, cancel := context.WithTimeout(context.Background(), imageLeaseCleanupTimeout)
	defer cancel()
	inspected, err := dockerClient.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect image lease %q: %w", name, err)
	}
	if inspected.Container.Config == nil || inspected.Container.Config.Labels[imageLeaseOwnerLabel] != owner {
		return fmt.Errorf("refuse to remove image lease %q: ownership label mismatch", name)
	}
	if inspected.Container.ID == "" {
		return fmt.Errorf("refuse to remove image lease %q: empty container ID", name)
	}
	_, err = dockerClient.ContainerRemove(ctx, inspected.Container.ID, client.ContainerRemoveOptions{})
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove image lease %q: %w", name, err)
	}
	return nil
}
