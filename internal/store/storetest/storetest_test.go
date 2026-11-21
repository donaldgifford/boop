//go:build integration

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

package storetest_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/boop/internal/store/storetest"
)

func TestStart(t *testing.T) {
	var id string

	t.Run("serves the pinned major version", func(t *testing.T) {
		srv := storetest.Start(t)
		id = srv.Container

		conn, err := pgx.Connect(t.Context(), srv.DSN)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer conn.Close(t.Context())

		var version string
		if err := conn.QueryRow(t.Context(), "SHOW server_version").Scan(&version); err != nil {
			t.Fatalf("server_version: %v", err)
		}
		if !strings.HasPrefix(version, "17.") {
			t.Errorf("server_version = %q, want 17.x", version)
		}
	})

	// The subtest's cleanup has run, so the container is gone.
	out, _ := exec.CommandContext(t.Context(), "docker", "ps", "--quiet", "--filter", "id="+id).Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("container %s still running after the test", id)
	}
}
