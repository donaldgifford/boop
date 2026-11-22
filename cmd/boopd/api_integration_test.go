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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/boop/api"
	"github.com/donaldgifford/boop/internal/api/oidctest"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
)

// TestAPI_ServeAndStop runs `boopd api` against a migrated store and a
// local issuer: it becomes ready once the schema checks and discovery
// succeeds, serves the contract without a token, answers /me for a
// valid token and 401 without one, and stops cleanly.
func TestAPI_ServeAndStop(t *testing.T) {
	db := storetest.Start(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := store.Migrate(t.Context(), db.OwnerDSN(), quiet); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	iss := oidctest.Start(t, "boopd-api")
	path := apiConfig(t, db.RoleDSN(storetest.RoleRO), iss.URL)

	t.Setenv("TEMPORAL_ADDRESS", "127.0.0.1:1") // lazy: never dialled here
	t.Setenv("TEMPORAL_TLS_DISABLED", "true")
	t.Setenv("LISTEN_ADDR", "127.0.0.1:0")
	t.Setenv("METRICS_ADDR", "127.0.0.1:0")

	ctx, cancel := context.WithCancel(t.Context())
	addrc := make(chan string, 1)
	done := make(chan error, 1)
	var stdout, stderr strings.Builder
	go func() { done <- runAPIRole(ctx, path, &stdout, &stderr, func(a string) { addrc <- a }) }()
	var base string
	select {
	case addr := <-addrc:
		base = "http://" + addr
	case err := <-done:
		cancel()
		t.Fatalf("api ended before listening: %v\n%s", err, stderr.String())
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("api did not listen")
	}
	waitReady(t, base)

	code, body := apiGet(t, base+"/api/v1/openapi.yaml", "")
	if code != http.StatusOK || !bytes.Equal(body, api.Spec) {
		t.Errorf("GET /openapi.yaml = %d (%d bytes), want 200 and the embedded spec", code, len(body))
	}
	if code, _ := apiGet(t, base+"/api/v1/me", ""); code != http.StatusUnauthorized {
		t.Errorf("GET /me without a token = %d, want 401", code)
	}
	code, body = apiGet(t, base+"/api/v1/me", iss.Valid(t, "platform"))
	var me struct {
		AllOwners bool     `json:"all_owners"`
		Operator  bool     `json:"operator"`
		Owners    []string `json:"owners"`
	}
	if err := json.Unmarshal(body, &me); code != http.StatusOK || err != nil {
		t.Fatalf("GET /me = %d %s (%v), want 200", code, body, err)
	}
	if !me.AllOwners || !me.Operator || me.Owners == nil {
		t.Errorf("/me = %+v, want every owner, operator, and an empty owners list", me)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("api did not stop")
	}
	if !strings.Contains(stdout.String(), "api stopped") {
		t.Errorf("no stop log:\n%s", stdout.String())
	}
}

// TestAPI_RefusesWithoutAuth: a file without api.auth never serves.
func TestAPI_RefusesWithoutAuth(t *testing.T) {
	t.Parallel()
	path := exampleWith(t, func(s string) string { return s })
	var stdout, stderr strings.Builder
	if err := runAPIRole(t.Context(), path, &stdout, &stderr, nil); !errors.Is(err, errNoAPIAuth) {
		t.Errorf("runAPIRole = %v, want %v", err, errNoAPIAuth)
	}
}

// apiConfig writes the example config with the read-only DSN, an api
// block for issuer and an authz file granting group platform everything.
func apiConfig(t *testing.T, roDSN, issuer string) string {
	t.Helper()
	secrets := t.TempDir()
	writeSecret(t, secrets, "boopd-store", "ro-dsn", roDSN)
	authz := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(authz, []byte("owners:\n  platform: [\"*\"]\noperators: [platform]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return exampleWith(t, func(s string) string {
		s = strings.Replace(s, `secrets_dir = "/etc/boopd/secrets"`, `secrets_dir = "`+secrets+`"`, 1)
		return s + `
store {
  ro_dsn_secret_ref {
    name = "boopd-store"
    key  = "ro-dsn"
  }
}

api {
  auth {
    issuer   = "` + issuer + `"
    audience = "boopd-api"
  }
  authz_file = "` + authz + `"
}
`
	})
}

func apiGet(t *testing.T, url, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}
