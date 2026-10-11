package observability_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/observability"
	"github.com/donaldgifford/boop/internal/temporal"
)

// TestMeterProvider_ExpositionNames: scraped names are the instrument
// names exactly, with no unit or counter suffix added.
func TestMeterProvider_ExpositionNames(t *testing.T) {
	t.Parallel()
	mp, handler, err := observability.NewMeterProvider(temporal.MetricViews()...)
	if err != nil {
		t.Fatalf("NewMeterProvider: %v", err)
	}
	m, err := observability.NewMetrics(mp)
	if err != nil {
		t.Fatal(err)
	}
	m.RunCompleted("Succeeded", "done", "default", time.Minute, time.Second, time.Second)
	m.TokenMint("success")
	m.RecordRequest("get", "pods", "200")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE boopd_runs_total counter",
		"# TYPE boopd_run_duration_seconds histogram",
		"# TYPE boopd_token_mints_total counter",
		"# TYPE boopd_kube_requests_total counter",
		`boopd_runs_total{outcome="Succeeded",profile="default",result="done"} 1`,
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition lacks %q", want)
		}
	}
	if strings.Contains(body, "boopd_runs_total_total") || strings.Contains(body, "seconds_seconds") {
		t.Error("exposition added a suffix to a boopd name")
	}
}

func TestNewLogger(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log, err := observability.NewLogger(&buf, "warn")
	if err != nil {
		t.Fatal(err)
	}
	log.Info("dropped")
	log.Warn("kept", "k", "v")
	if out := buf.String(); strings.Contains(out, "dropped") || !strings.Contains(out, `"msg":"kept"`) {
		t.Errorf("log output = %q, want JSON at warn and above", out)
	}
	if _, err := observability.NewLogger(io.Discard, "loud"); err == nil {
		t.Error("NewLogger(loud) err = nil")
	}
	if _, err := observability.NewLogger(io.Discard, ""); err != nil {
		t.Errorf("NewLogger(\"\") = %v, want info", err)
	}
}

func TestHealth(t *testing.T) {
	t.Parallel()
	h := observability.NewHealth()
	var ready atomic.Bool
	h.Add(observability.Check{Name: "temporal", Check: func(context.Context) error {
		if !ready.Load() {
			return context.DeadlineExceeded
		}
		return nil
	}})
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- observability.ServeListener(ctx, ln, h.Handler()) }()
	base := "http://" + ln.Addr().String()

	get := func(path string) int {
		t.Helper()
		resp, err := http.Get(base + path) //nolint:noctx // test against a local server
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("/healthz"); c != http.StatusOK {
		t.Errorf("/healthz = %d", c)
	}
	if c := get("/readyz"); c != http.StatusServiceUnavailable {
		t.Errorf("/readyz before ready = %d, want 503", c)
	}
	ready.Store(true)
	if c := get("/readyz"); c != http.StatusOK {
		t.Errorf("/readyz when ready = %d, want 200", c)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("ServeListener after cancel: %v", err)
	}
}
