//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/ingest"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
	"github.com/donaldgifford/boop/internal/temporal/temporaltest"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// TestIngest_StartPingStop starts `boopd ingest` against the dev server,
// answers a signed ping, and stops cleanly when its context ends, as on
// SIGTERM.
func TestIngest_StartPingStop(t *testing.T) {
	secrets := webhookSecrets(t)
	path := exampleWith(t, func(s string) string {
		return strings.Replace(s, `secrets_dir = "/etc/boopd/secrets"`, `secrets_dir = "`+secrets+`"`, 1)
	})
	ig := startIngest(t, path)
	if code := ig.ping(t, "d-ping"); code != http.StatusOK {
		t.Errorf("ping = %d, want 200", code)
	}
	ig.stop(t)
}

// TestIngest_StoreRecordsDelivery runs ingest with store.enabled: it
// connects as boopd_ingest after the schema check, and a delivery lands
// in webhook_deliveries.
func TestIngest_StoreRecordsDelivery(t *testing.T) {
	db := storetest.Start(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	secrets := webhookSecrets(t)
	writeSecret(t, secrets, "boopd-store", "worker-dsn", db.RoleDSN(storetest.RoleWorker))
	writeSecret(t, secrets, "boopd-store", "ingest-dsn", db.RoleDSN(storetest.RoleIngest))
	path := exampleWith(t, func(s string) string {
		s = strings.Replace(s, `secrets_dir = "/etc/boopd/secrets"`, `secrets_dir = "`+secrets+`"`, 1)
		return s + `
store {
  enabled               = true
  dsn_secret_ref {
    name = "boopd-store"
    key  = "worker-dsn"
  }
  ingest_dsn_secret_ref {
    name = "boopd-store"
    key  = "ingest-dsn"
  }
}
`
	})

	t.Run("refuses an unmigrated schema", func(t *testing.T) {
		var stdout, stderr strings.Builder
		err := runIngestRole(t.Context(), path, &stdout, &stderr, nil)
		if err == nil || !strings.Contains(err.Error(), "store_meta") {
			t.Errorf("runIngestRole before migrate = %v, want a schema error", err)
		}
	})

	if _, err := store.Migrate(t.Context(), db.OwnerDSN(), quiet); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ig := startIngest(t, path)
	const guid = "6a0c1f5e-0000-4000-8000-000000000001"
	if code := ig.ping(t, guid); code != http.StatusOK {
		t.Errorf("ping = %d, want 200", code)
	}
	ig.stop(t)

	owner, err := store.Open(t.Context(), db.OwnerDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	var n int
	if err := owner.QueryRow(t.Context(),
		"SELECT count(*) FROM webhook_deliveries WHERE delivery_id = $1 AND event = 'ping'", guid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("webhook_deliveries rows for %s = %d, want 1", guid, n)
	}
}

func webhookSecrets(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeSecret(t, dir, "boop-bot-webhook", "secret", "s3cret\n")
	return dir
}

func writeSecret(t *testing.T, dir, name, key, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, key), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runningIngest is `boopd ingest` running against the dev server.
type runningIngest struct {
	base   string
	cancel context.CancelFunc
	done   chan error
	stdout *strings.Builder
}

func startIngest(t *testing.T, path string) *runningIngest {
	t.Helper()
	srv := temporaltest.Start(t)
	t.Setenv("TEMPORAL_ADDRESS", srv.Config.Address)
	t.Setenv("TEMPORAL_TLS_DISABLED", "true")
	t.Setenv("LISTEN_ADDR", "127.0.0.1:0")
	t.Setenv("METRICS_ADDR", "127.0.0.1:0")

	ctx, cancel := context.WithCancel(t.Context())
	addrc := make(chan string, 1)
	ig := &runningIngest{cancel: cancel, done: make(chan error, 1), stdout: &strings.Builder{}}
	var stderr strings.Builder
	go func() {
		ig.done <- runIngestRole(ctx, path, ig.stdout, &stderr, func(a string) { addrc <- a })
	}()
	select {
	case addr := <-addrc:
		ig.base = "http://" + addr
	case err := <-ig.done:
		cancel()
		t.Fatalf("ingest ended before listening: %v\n%s", err, stderr.String())
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("ingest did not listen")
	}
	waitReady(t, ig.base)
	return ig
}

// ping posts a signed ping with the delivery ID and returns the status.
func (ig *runningIngest) ping(t *testing.T, delivery string) int {
	t.Helper()
	body := []byte(`{"zen":"Design for failure.","hook_id":1}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ig.base+"/webhooks/github", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ingest.HeaderEvent, "ping")
	req.Header.Set(ingest.HeaderDelivery, delivery)
	req.Header.Set(ingest.HeaderTargetID, "123456")
	req.Header.Set(ingest.HeaderSignature, fakegithub.Sign("s3cret", body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// stop cancels the context, as SIGTERM does, and waits for a clean stop.
func (ig *runningIngest) stop(t *testing.T) {
	t.Helper()
	ig.cancel()
	select {
	case err := <-ig.done:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("ingest did not stop")
	}
	if !strings.Contains(ig.stdout.String(), "ingest stopped") {
		t.Errorf("no stop log:\n%s", ig.stdout.String())
	}
}

func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/readyz", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("ingest never ready")
}
