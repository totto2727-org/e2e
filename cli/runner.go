package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

const (
	maxParallelScenarios     = 2
	imageLeaseCleanupTimeout = 30 * time.Second
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
			ctx, cancel := context.WithTimeout(t.Context(), scenarioTimeout)
			defer cancel()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			t.Log("started")
			t.Cleanup(func() { <-slots })
			t.Cleanup(func() { t.Logf("completed pass=%t", !t.Failed()) })
			container, err := testcontainers.Run(ctx, imageID)
			testcontainers.CleanupContainer(t, container)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("container=%s", container.GetContainerID())
			testCase.Run(t, &Environment{ctx: ctx, container: container})
		})
	}
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
