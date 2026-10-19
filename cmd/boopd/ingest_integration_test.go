//go:build integration

package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/ingest"
	"github.com/donaldgifford/boop/internal/temporal/temporaltest"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// TestIngest_StartPingStop starts `boopd ingest` against the dev server,
// answers a signed ping, and stops cleanly when its context ends, as on
// SIGTERM.
func TestIngest_StartPingStop(t *testing.T) {
	srv := temporaltest.Start(t)
	secrets := t.TempDir()
	if err := os.MkdirAll(filepath.Join(secrets, "boop-bot-webhook"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "boop-bot-webhook", "secret"), []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := exampleWith(t, func(s string) string {
		return strings.Replace(s, `secrets_dir = "/etc/boopd/secrets"`, `secrets_dir = "`+secrets+`"`, 1)
	})
	t.Setenv("TEMPORAL_ADDRESS", srv.Config.Address)
	t.Setenv("TEMPORAL_TLS_DISABLED", "true")
	t.Setenv("LISTEN_ADDR", "127.0.0.1:0")
	t.Setenv("METRICS_ADDR", "127.0.0.1:0")

	ctx, cancel := context.WithCancel(t.Context())
	addrc := make(chan string, 1)
	done := make(chan error, 1)
	var stdout, stderr strings.Builder
	go func() {
		done <- runIngestRole(ctx, path, &stdout, &stderr, func(a string) { addrc <- a })
	}()
	var addr string
	select {
	case addr = <-addrc:
	case err := <-done:
		t.Fatalf("ingest ended before listening: %v\n%s", err, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatal("ingest did not listen")
	}
	base := "http://" + addr

	waitReady(t, base)
	body := []byte(`{"zen":"Design for failure.","hook_id":1}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/webhooks/github", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ingest.HeaderEvent, "ping")
	req.Header.Set(ingest.HeaderDelivery, "d-ping")
	req.Header.Set(ingest.HeaderTargetID, "123456")
	req.Header.Set(ingest.HeaderSignature, fakegithub.Sign("s3cret", body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("ping = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("ingest did not stop")
	}
	if !strings.Contains(stdout.String(), "ingest stopped") {
		t.Errorf("no stop log:\n%s", stdout.String())
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
