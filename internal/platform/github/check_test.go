package github_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/donaldgifford/boop/internal/platform"
)

func TestCheckRepo(t *testing.T) {
	t.Parallel()

	repo := func(archived bool) http.HandlerFunc {
		body := `{"id":5,"node_id":"R_5","full_name":"o/r","default_branch":"trunk","archived":false}`
		if archived {
			body = `{"id":5,"node_id":"R_5","full_name":"o/r","default_branch":"trunk","archived":true}`
		}
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
	}
	status := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { http.Error(w, `{"message":"x"}`, code) }
	}
	config := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ref") != "trunk" {
			http.Error(w, "wrong ref", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"type":"file","name":"renovate.json","content":"e30K","encoding":"base64"}`))
	}
	const (
		repoRoute   = "GET /api/v3/repositories/5"
		configRoute = "GET /api/v3/repos/o/r/contents/renovate.json"
	)

	tests := []struct {
		name     string
		handlers map[string]http.HandlerFunc
		want     platform.RepoState
		wantRepo bool
		wantErr  error
	}{
		{
			name:     "present",
			handlers: map[string]http.HandlerFunc{repoRoute: repo(false), configRoute: config},
			want:     platform.RepoPresent,
			wantRepo: true,
		},
		{name: "no config", handlers: map[string]http.HandlerFunc{repoRoute: repo(false)}, want: platform.RepoNoConfig, wantRepo: true},
		{name: "gone", handlers: map[string]http.HandlerFunc{}, want: platform.RepoGone},
		{
			name:     "archived",
			handlers: map[string]http.HandlerFunc{repoRoute: repo(true), configRoute: config},
			want:     platform.RepoGone,
			wantRepo: true,
		},
		{
			name:     "outage",
			handlers: map[string]http.HandlerFunc{repoRoute: status(http.StatusBadGateway)},
			want:     platform.RepoUnknown,
			wantErr:  platform.ErrTransient,
		},
		{
			name:     "bad credentials",
			handlers: map[string]http.HandlerFunc{repoRoute: status(http.StatusUnauthorized)},
			want:     platform.RepoUnknown,
			wantErr:  platform.ErrUnauthorized,
		},
		{
			name:     "config probe outage",
			handlers: map[string]http.HandlerFunc{repoRoute: repo(false), configRoute: status(http.StatusInternalServerError)},
			want:     platform.RepoUnknown, wantErr: platform.ErrTransient,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newFakeClient(t, tt.handlers)
			got, r, err := c.CheckRepo(context.Background(), 5)
			if got != tt.want {
				t.Errorf("CheckRepo(5) = %v, want %v", got, tt.want)
			}
			switch {
			case tt.wantErr == nil && err != nil:
				t.Errorf("CheckRepo(5) err = %v, want nil", err)
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Errorf("CheckRepo(5) err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantRepo && (r == nil || r.ID != 5 || r.Slug != "o/r" || r.DefaultBranch != "trunk") {
				t.Errorf("CheckRepo(5) repo = %+v, want id 5 o/r@trunk", r)
			}
		})
	}
}

func TestCheckRepo_InvalidID(t *testing.T) {
	t.Parallel()
	if _, _, err := newFakeClient(t, nil).CheckRepo(context.Background(), 0); err == nil {
		t.Error("CheckRepo(0) err = nil, want error")
	}
}

func TestRepoState_String(t *testing.T) {
	t.Parallel()
	for s, want := range map[platform.RepoState]string{
		platform.RepoUnknown: "unknown", platform.RepoGone: "gone",
		platform.RepoNoConfig: "no-config", platform.RepoPresent: "present",
	} {
		if got := s.String(); got != want {
			t.Errorf("RepoState(%d).String() = %q, want %q", int(s), got, want)
		}
	}
}
