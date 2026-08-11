package cli

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

const (
	maxParallelScenarios = 2
	imageBuildTimeout    = 5 * time.Minute
	scenarioTimeout      = time.Minute
)

var imageSequence atomic.Uint64

type ImageConfig struct {
	Context    string
	Dockerfile string
}

type Case struct {
	Name string
	Run  func(*testing.T, *Environment)
}

func Run(t *testing.T, image ImageConfig, cases []Case) {
	t.Helper()
	imageName := buildImage(t, image)
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
			container, err := testcontainers.Run(ctx, imageName)
			testcontainers.CleanupContainer(t, container)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("container=%s", container.GetContainerID())
			testCase.Run(t, &Environment{ctx: ctx, container: container})
		})
	}
}

func buildImage(t *testing.T, image ImageConfig) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), imageBuildTimeout)
	defer cancel()
	repo := fmt.Sprintf("go-cli-e2e-%d", os.Getpid())
	tag := fmt.Sprintf("%d-%d", time.Now().UnixNano(), imageSequence.Add(1))
	imageName := repo + ":" + tag
	owner, err := testcontainers.Run(ctx, "", testcontainers.WithDockerfile(testcontainers.FromDockerfile{
		Context: image.Context, Dockerfile: image.Dockerfile, Repo: repo, Tag: tag, KeepImage: false,
	}))
	testcontainers.CleanupContainer(t, owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("image=%s image_owner=%s", imageName, owner.GetContainerID())
	return imageName
}
