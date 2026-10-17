package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

// dockerRuntime owns exec connections instead of delegating to the upstream
// Exec helper, which waits for output before checking the context again.
type dockerRuntime struct {
	*testcontainers.DockerContainer
	executor dockerExecutor
}

type dockerExecutor interface {
	ExecCreate(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error)
}

func (c *dockerRuntime) Run(ctx context.Context, command Command) (Result, error) {
	return runCommand(ctx, c.executor, c.GetContainerID(), command)
}

func runCommand(ctx context.Context, executor dockerExecutor, containerID string, command Command) (Result, error) {
	created, err := executor.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd: command.Args, WorkingDir: command.WorkingDir, Env: command.Env,
		AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return Result{}, fmt.Errorf("create exec: %w", errors.Join(ctx.Err(), err))
	}
	attached, err := executor.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return Result{}, fmt.Errorf("attach exec: %w", errors.Join(ctx.Err(), err))
	}
	defer attached.Close()
	stop := context.AfterFunc(ctx, attached.Close)
	defer stop()

	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, attached.Reader)
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err := errors.Join(ctx.Err(), err); err != nil {
		return result, fmt.Errorf("read exec output: %w", err)
	}
	for {
		inspected, err := executor.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err := errors.Join(ctx.Err(), err); err != nil {
			return result, fmt.Errorf("inspect exec: %w", err)
		}
		if !inspected.Running {
			result.ExitCode = inspected.ExitCode
			return result, nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, fmt.Errorf("wait for exec: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// newExecClient preserves Testcontainers' daemon and TLS dialer while making
// cancellation close the connection during the HTTP upgrade as well as output
// reads. The separate client avoids mutating the caller's transport.
func newExecClient(ctx context.Context, base *client.Client) (*client.Client, error) {
	// A newly created Testcontainers client has not necessarily made a request.
	// Resolve its lazy API negotiation before cloning the effective version.
	// Ping respects an explicit DOCKER_API_VERSION override.
	if _, err := base.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		return nil, fmt.Errorf("negotiate exec API version: %w", err)
	}
	dial := base.Dialer()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		owned := &contextConnection{Conn: conn}
		owned.stop = context.AfterFunc(ctx, owned.close)
		return owned, nil
	}}
	executor, err := client.New(
		client.WithHost(base.DaemonHost()),
		client.WithAPIVersion(base.ClientVersion()),
		client.WithHTTPClient(&http.Client{Transport: transport}),
	)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("create exec client: %w", err)
	}
	return executor, nil
}

type contextConnection struct {
	net.Conn
	stop     func() bool
	once     sync.Once
	closeErr error
}

func (c *contextConnection) close() {
	c.once.Do(func() { c.closeErr = c.Conn.Close() })
}

func (c *contextConnection) Close() error {
	if c.stop != nil {
		c.stop()
	}
	c.close()
	return c.closeErr
}
