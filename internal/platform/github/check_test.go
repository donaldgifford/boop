package github_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"golang.org/x/time/rate"

	"github.com/donaldgifford/boop/internal/platform"
	ghclient "github.com/donaldgifford/boop/internal/platform/github"
	"github.com/donaldgifford/boop/test/fakegithub"
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
		// The root listing; renovate.json5 is one of the default paths.
		_, _ = w.Write([]byte(`[{"type":"file","name":"README.md"},{"type":"file","name":"renovate.json5"}]`))
	}
	file := func(w http.ResponseWriter, _ *http.Request) {
		body := "{extends: ['config:recommended']}"
		_, _ = fmt.Fprintf(w, `{"type":"file","encoding":"base64","size":%d,"name":"renovate.json5","path":"renovate.json5","content":%q}`,
			len(body), base64.StdEncoding.EncodeToString([]byte(body)))
	}
	const (
		repoRoute   = "GET /api/v3/repositories/5"
		configRoute = "GET /api/v3/repos/o/r/contents/"
		fileRoute   = "GET /api/v3/repos/o/r/contents/renovate.json5"
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
			handlers: map[string]http.HandlerFunc{repoRoute: repo(false), configRoute: config, fileRoute: file},
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
			check, err := c.CheckRepo(context.Background(), 5)
			got, r := check.State, check.Repo
			if got != tt.want {
				t.Errorf("CheckRepo(5) = %v, want %v", got, tt.want)
			}
			if wantExtends := tt.want == platform.RepoPresent; wantExtends != slices.Equal(check.Config.Extends, []string{"config:recommended"}) {
				t.Errorf("CheckRepo(5) config = %+v", check.Config)
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
	if _, err := newFakeClient(t, nil).CheckRepo(context.Background(), 0); err == nil {
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

func TestRepoIDBySlug(t *testing.T) {
	t.Parallel()
	c := newFakeClient(t, map[string]http.HandlerFunc{
		"GET /api/v3/repos/boop-bot/renovate-config": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":4242,"full_name":"boop-bot/renovate-config"}`))
		},
	})
	id, err := c.RepoIDBySlug(context.Background(), "boop-bot/renovate-config")
	if err != nil || id != 4242 {
		t.Errorf("RepoIDBySlug() = %d, %v; want 4242", id, err)
	}
	if _, err := c.RepoIDBySlug(context.Background(), "boop-bot/missing"); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("RepoIDBySlug(missing) err = %v, want ErrNotFound", err)
	}
	if _, err := c.RepoIDBySlug(context.Background(), "noslash"); err == nil {
		t.Error("RepoIDBySlug(noslash) err = nil")
	}
}

// appClient is an installation client of fake installation 1 that mints
// its token from the fake, as the worker's are.
func appClient(t *testing.T, gh *fakegithub.Server) *ghclient.Client {
	t.Helper()
	c, err := ghclient.NewWithApp(ghclient.AppAuth{AppID: 1, InstallationID: 1, PEM: gh.PEM, BaseURL: gh.BaseURL()},
		ghclient.WithRateLimit(rate.Inf, 1))
	if err != nil {
		t.Fatalf("NewWithApp: %v", err)
	}
	return c
}

// CheckRepo against the fake GitHub: present with its extends, no
// config, archived, deleted, and a deleted installation.
func TestCheckRepo_AgainstFake(t *testing.T) {
	t.Parallel()
	gh := fakegithub.New(t)
	gh.SetRepos(
		fakegithub.Repo{
			ID: 1, NodeID: "R_1", Slug: "acme/on", DefaultBranch: "main", HasConfig: true,
			ConfigPath: "renovate.json5", Config: "{\n  // shared\n  extends: ['config:recommended', 'local>acme/preset'],\n}\n",
		},
		fakegithub.Repo{ID: 2, NodeID: "R_2", Slug: "acme/off", DefaultBranch: "main"},
		fakegithub.Repo{ID: 3, NodeID: "R_3", Slug: "acme/old", DefaultBranch: "main", Archived: true, HasConfig: true},
	)
	c := appClient(t, gh)
	for _, tt := range []struct {
		name    string
		id      int64
		want    platform.RepoState
		extends []string
	}{
		{"present", 1, platform.RepoPresent, []string{"config:recommended", "local>acme/preset"}},
		{"no config", 2, platform.RepoNoConfig, nil},
		{"archived", 3, platform.RepoGone, nil},
		{"deleted", 4, platform.RepoGone, nil},
	} {
		check, err := c.CheckRepo(context.Background(), tt.id)
		if err != nil {
			t.Errorf("%s: CheckRepo(%d) err = %v", tt.name, tt.id, err)
			continue
		}
		if check.State != tt.want || !slices.Equal(check.Config.Extends, tt.extends) {
			t.Errorf("%s: CheckRepo(%d) = %v %+v, want %v with extends %q", tt.name, tt.id, check.State, check.Config, tt.want, tt.extends)
		}
	}
}

func TestCheckRepo_InstallationDeleted(t *testing.T) {
	t.Parallel()
	gh := fakegithub.New(t)
	gh.SetRepos(fakegithub.Repo{ID: 1, NodeID: "R_1", Slug: "acme/on", DefaultBranch: "main", HasConfig: true})
	gh.DeleteInstallation(1)
	check, err := appClient(t, gh).CheckRepo(context.Background(), 1)
	if !errors.Is(err, platform.ErrInstallationGone) {
		t.Fatalf("CheckRepo() err = %v, want ErrInstallationGone", err)
	}
	if check.State != platform.RepoUnknown {
		t.Errorf("state = %v, want unknown with the error", check.State)
	}
}

// A run's scoped mint for a deleted installation is ErrInstallationGone;
// any other mint failure is not.
func TestMint_InstallationDeleted(t *testing.T) {
	t.Parallel()
	gh := fakegithub.New(t)
	gh.DeleteInstallation(1)
	app, err := ghclient.NewAppClient(1, gh.PEM, gh.BaseURL())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.Mint(context.Background(), 1, []int64{5}); !errors.Is(err, platform.ErrInstallationGone) {
		t.Errorf("Mint() err = %v, want ErrInstallationGone", err)
	}
	if _, _, err := app.Mint(context.Background(), 2, []int64{5}); err != nil {
		t.Errorf("Mint() for a live installation err = %v", err)
	}
}
