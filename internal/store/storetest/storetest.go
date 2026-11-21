/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package storetest starts a Postgres container for tests. It is
// imported only from tests under the integration build tag.
//
// The container runs a pinned image through the docker CLI, so a test
// run never silently moves to a new server, and it is removed when the
// test finishes.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Image is the pinned Postgres image (IMPL-0005 OQ2).
const Image = "postgres:17.12-alpine"

const (
	user     = "boopd"
	password = "boopd"
	database = "boopd"

	// readyTimeout bounds the wait for the server to accept connections.
	readyTimeout = 60 * time.Second
)

// Server is a running Postgres container.
type Server struct {
	// DSN connects as the superuser, which owns the database.
	DSN string
	// Container is the docker container ID.
	Container string
}

// Start runs Postgres in a container on a free host port, waits until it
// accepts connections, and removes the container when tb finishes.
func Start(tb testing.TB) *Server {
	tb.Helper()

	ctx := tb.Context()

	out, err := exec.CommandContext(ctx, "docker", "run", "--detach", "--rm",
		"--env", "POSTGRES_USER="+user,
		"--env", "POSTGRES_PASSWORD="+password,
		"--env", "POSTGRES_DB="+database,
		"--publish", "127.0.0.1::5432",
		Image,
	).Output()
	if err != nil {
		tb.Fatalf("storetest: start %s: %v", Image, stderr(err))
	}

	id := strings.TrimSpace(string(out))
	tb.Cleanup(func() {
		// tb's context is done by now, so the removal gets its own.
		//nolint:gosec // G204: id is the container docker returned.
		if err := exec.CommandContext(context.Background(), "docker", "rm", "--force", id).Run(); err != nil {
			tb.Logf("storetest: remove container %s: %v", id, err)
		}
	})

	port, err := hostPort(ctx, id)
	if err != nil {
		tb.Fatalf("storetest: %v", err)
	}

	srv := &Server{
		DSN: fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
			user, password, net.JoinHostPort("127.0.0.1", port), database),
		Container: id,
	}
	if err := waitReady(ctx, srv.DSN); err != nil {
		tb.Fatalf("storetest: %s did not become ready: %v", Image, err)
	}

	return srv
}

// hostPort reads the host port docker bound to the container's 5432.
func hostPort(ctx context.Context, id string) (string, error) {
	//nolint:gosec // G204: id is the container docker returned.
	out, err := exec.CommandContext(ctx, "docker", "port", id, "5432/tcp").Output()
	if err != nil {
		return "", fmt.Errorf("read port of %s: %w", id, stderr(err))
	}

	// One line per bound address, such as "127.0.0.1:55012".
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")

	_, port, err := net.SplitHostPort(line)
	if err != nil {
		return "", fmt.Errorf("parse port %q: %w", line, err)
	}

	return port, nil
}

// waitReady polls until a connection succeeds. The image's entrypoint
// restarts the server once after initdb, so one success is not enough
// on its own; a query proves the final server is up.
func waitReady(ctx context.Context, dsn string) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	var last error
	for {
		if last = ping(ctx, dsn); last == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (last error: %w)", ctx.Err(), last)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func ping(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("query: %w", err)
	}

	return nil
}

// stderr adds the command's stderr to an exec error.
func stderr(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
	}

	return err
}
