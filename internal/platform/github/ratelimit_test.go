package github_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/workflows"
)

const rateLimitBody = `{
  "resources": {
    "core":    {"limit": 12500, "used": 500, "remaining": 12000, "reset": 1791637200},
    "graphql": {"limit": 5000,  "used": 10,  "remaining": 4990,  "reset": 1791637300},
    "search":  {"limit": 30,    "used": 0,   "remaining": 30,    "reset": 1791633660}
  },
  "rate": {"limit": 12500, "used": 500, "remaining": 12000, "reset": 1791637200}
}`

func TestReadRateLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		date string
		want time.Time
	}{
		{name: "date header", date: "Sat, 10 Oct 2026 12:30:00 GMT", want: time.Date(2026, 10, 10, 12, 30, 0, 0, time.UTC)},
		{name: "no date header"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v3/rate_limit" {
					http.NotFound(w, r)
					return
				}
				if tt.date != "" {
					w.Header().Set("Date", tt.date)
				} else {
					w.Header()["Date"] = nil // suppress net/http's automatic Date
				}
				_, _ = w.Write([]byte(rateLimitBody))
			})
			before := time.Now().UTC().Add(-time.Second)
			got, err := newProbeClient(t, h).ReadRateLimit(context.Background())
			if err != nil {
				t.Fatalf("ReadRateLimit() err = %v", err)
			}
			if len(got.Resources) != 2 {
				t.Errorf("ReadRateLimit() resources = %v, want core and graphql only", got.Resources)
			}
			core := got.Resources[workflows.ResourceCore]
			if core.Limit != 12500 || core.Remaining != 12000 || !core.Reset.Equal(time.Unix(1791637200, 0)) {
				t.Errorf("core = %+v, want 12500/12000/1791637200", core)
			}
			gql := got.Resources[workflows.ResourceGraphQL]
			if gql.Limit != 5000 || gql.Remaining != 4990 || !gql.Reset.Equal(time.Unix(1791637300, 0)) {
				t.Errorf("graphql = %+v, want 5000/4990/1791637300", gql)
			}
			if tt.date != "" && !got.ObservedAt.Equal(tt.want) {
				t.Errorf("ObservedAt = %v, want %v from Date", got.ObservedAt, tt.want)
			}
			if tt.date == "" && got.ObservedAt.Before(before) {
				t.Errorf("ObservedAt = %v, want local now without a Date header", got.ObservedAt)
			}
		})
	}
}

func TestReadRateLimit_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{name: "missing graphql", status: http.StatusOK, body: `{"resources":{"core":{"limit":1,"remaining":1,"reset":1}}}`},
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`, wantErr: platform.ErrUnauthorized},
		{name: "server error", status: http.StatusBadGateway, body: `{}`, wantErr: platform.ErrTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := newProbeClient(t, h).ReadRateLimit(context.Background())
			if err == nil {
				t.Fatal("ReadRateLimit() err = nil, want error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("ReadRateLimit() err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
