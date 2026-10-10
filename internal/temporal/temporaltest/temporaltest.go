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

// Start runs a dev server with the boopd namespace registered and stops
// it when tb finishes. The first run on a machine downloads the CLI
// into the user cache directory.
func Start(tb testing.TB) *Server {
	tb.Helper()

	quiet := tlog.NewStructuredLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv, err := testsuite.StartDevServer(tb.Context(), testsuite.DevServerOptions{
		CachedDownload: testsuite.CachedDownload{Version: CLIVersion},
		ClientOptions:  &client.Options{Namespace: temporal.DefaultNamespace, Logger: quiet},
		LogLevel:       "error",
	})
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
