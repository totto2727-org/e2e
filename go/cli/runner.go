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
	imageLeaseCleanupTimeout = 30 * time.Second
	containerCleanupTimeout  = 30 * time.Second
	scenarioTimeout          = time.Minute
	imageLeaseOwnerLabel     = "io.github.totto2727-org.e2e.image-lease-owner"
)

// NewEnvironment prepares an isolated environment for the calling test.
// Call it inside each Test or t.Run callback, after any caller-owned t.Parallel.
// The one-minute budget includes setup. Test cleanup cancels execution before
// closing clients and removing the container and its immutable image lease.
func NewEnvironment(t *testing.T, image string) *Environment {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), scenarioTimeout)
	// Register last even when setup calls Fatal: cancellation must precede
	// client closure, container termination, and image-lease release.
	defer t.Cleanup(cancel)
	imageID, releaseImage, err := retainLocalImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := releaseImage(); err != nil {
			t.Error(err)
		}
	})
	container, err := testcontainers.Run(ctx, imageID)
	// Run can return a partially created container alongside an error.
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
	return &Environment{ctx: ctx, container: &dockerRuntime{DockerContainer: container, executor: executor}}
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
