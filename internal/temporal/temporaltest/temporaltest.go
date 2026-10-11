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

// Package temporaltest starts a Temporal dev server for tests. It is
// imported only from tests.
//
// The server is the Temporal CLI's start-dev, downloaded once per
// machine at a pinned version so a test run never silently moves to a
// new server.
package temporaltest

import (
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"

	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/boop/internal/temporal"
)

// CLIVersion is the pinned Temporal CLI, and so dev server, version.
const CLIVersion = "v1.9.1"

// Server is a running dev server.
type Server struct {
	// Client is connected to the boopd namespace.
	Client client.Client
	// Config reaches the server the way the application would.
	Config temporal.Config
}

// Option configures Start.
type Option func(tb testing.TB, o *testsuite.DevServerOptions)

// ListenOnAllInterfaces binds the frontend to 0.0.0.0 on a free port, so
// a worker in a k3d cluster can dial it at host.k3d.internal:<port>.
func ListenOnAllInterfaces() Option {
	return func(tb testing.TB, o *testsuite.DevServerOptions) {
		tb.Helper()

		var lc net.ListenConfig
		l, err := lc.Listen(tb.Context(), "tcp", "0.0.0.0:0")
		if err != nil {
			tb.Fatalf("temporaltest: pick a port: %v", err)
		}

		addr, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			tb.Fatalf("temporaltest: listener address %v is not TCP", l.Addr())
		}

		port := addr.Port
		if err := l.Close(); err != nil {
			tb.Fatalf("temporaltest: release port: %v", err)
		}

		o.ClientOptions.HostPort = net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	}
}

// Start runs a dev server with the boopd namespace registered and stops
// it when tb finishes. The first run on a machine downloads the CLI
// into the user cache directory.
func Start(tb testing.TB, opts ...Option) *Server {
	tb.Helper()

	quiet := tlog.NewStructuredLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	o := testsuite.DevServerOptions{
		CachedDownload: testsuite.CachedDownload{Version: CLIVersion},
		ClientOptions:  &client.Options{Namespace: temporal.DefaultNamespace, Logger: quiet},
		LogLevel:       "error",
	}
	for _, opt := range opts {
		opt(tb, &o)
	}

	srv, err := testsuite.StartDevServer(tb.Context(), o)
	if err != nil {
		tb.Fatalf("temporaltest: start dev server %s: %v", CLIVersion, err)
	}

	tb.Cleanup(func() {
		if err := srv.Stop(); err != nil {
			tb.Logf("temporaltest: stop dev server: %v", err)
		}
	})

	return &Server{
		Client: srv.Client(),
		Config: temporal.Config{
			Address:   srv.FrontendHostPort(),
			Namespace: temporal.DefaultNamespace,
			TaskQueue: temporal.DefaultTaskQueue,
		},
	}
}
