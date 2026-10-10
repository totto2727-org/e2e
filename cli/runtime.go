package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
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

func (c *dockerRuntime) Exec(ctx context.Context, args []string, options ...tcexec.ProcessOption) (int, io.Reader, error) {
	return execCommand(ctx, c.executor, c.GetContainerID(), args, options...)
}

func execCommand(ctx context.Context, executor dockerExecutor, containerID string, args []string, options ...tcexec.ProcessOption) (int, io.Reader, error) {
	process := tcexec.NewProcessOptions(args)
	for _, option := range options {
		option.Apply(process)
	}
	created, err := executor.ExecCreate(ctx, containerID, process.ExecConfig)
	if err != nil {
		return 0, nil, fmt.Errorf("create exec: %w", errors.Join(ctx.Err(), err))
	}
	attached, err := executor.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return 0, nil, fmt.Errorf("attach exec: %w", errors.Join(ctx.Err(), err))
	}
	defer attached.Close()
	stop := context.AfterFunc(ctx, attached.Close)
	defer stop()

	process.Reader = attached.Reader
	for _, option := range options {
		option.Apply(process)
	}
	// Buffer before closing the owned connection, even if no output-processing
	// option was supplied. Never return a reader backed by a closed socket.
	output, err := io.ReadAll(process.Reader)
	if err := errors.Join(ctx.Err(), err); err != nil {
		return 0, nil, fmt.Errorf("read exec output: %w", err)
	}
	for {
		inspected, err := executor.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err := errors.Join(ctx.Err(), err); err != nil {
			return 0, nil, fmt.Errorf("inspect exec: %w", err)
		}
		if !inspected.Running {
			return inspected.ExitCode, bytes.NewReader(output), nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, nil, fmt.Errorf("wait for exec: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// newExecClient preserves Testcontainers' daemon and TLS dialer while making
// cancellation close the connection during the HTTP upgrade as well as output
// reads. The separate client avoids mutating the caller's transport.
func newExecClient(base *client.Client) (*client.Client, error) {
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
