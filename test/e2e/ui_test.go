//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/boop/test/fakegithub"
)

const (
	uiImageRepo = "ghcr.io/donaldgifford/boopd-ui"
	// The browser reaches the UI and the issuer through port-forwards on
	// fixed local ports: the UI's public URL and the issuer's redirect
	// carry them.
	uiLocalPort     = 3300
	issuerLocalPort = 9443
	// hiddenOwner is an installation's account the viewer cannot see.
	hiddenOwner = "globex"
)

// TestUI_Playwright (IMPL-0005 14.3) installs the store, the API and the
// UI, seeds a run, a bot pull request and a dashboard item, and runs the
// UI's Playwright suite (ui/e2e) against the stack from the host. It
// needs Bun and Playwright's Chromium on the host, so it runs only with
// BOOPD_E2E_UI=1 (CI's "UI E2E Tests" job; `just e2e-ui` locally).
func TestUI_Playwright(t *testing.T) {
	if os.Getenv("BOOPD_E2E_UI") != "1" {
		t.Skip("BOOPD_E2E_UI=1 runs the UI's Playwright suite")
	}
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	uiURL := fmt.Sprintf("http://localhost:%d", uiLocalPort)
	h := installStore(ctx, t, func(ctx context.Context, t *testing.T, ch *chartHarness, values map[string]any) {
		t.Helper()
		ch.secret(ctx, t, "boopd-ui", map[string][]byte{
			"client-secret": []byte("client-secret"),
			"session-keys":  []byte("e2e-session-key-0123456789abcdef0123456789"),
		})
		values["api"].(map[string]any)["authz"].(map[string]any)["owners"] = map[string]any{
			operatorGroup: []string{"*"},
			viewerGroup:   []string{ownerOf(runSlug)},
		}
		values["ui"] = map[string]any{
			"enabled": true, "replicaCount": 1,
			"image":          map[string]any{"repository": uiImageRepo, "tag": "dev", "pullPolicy": "Never"},
			"publicURL":      uiURL,
			"existingSecret": "boopd-ui",
			"oidc":           map[string]any{"issuer": fmt.Sprintf("https://mock-oidc.%s.svc:9443", ch.env.Namespace)},
			"extraEnv":       []any{map[string]any{"name": "NODE_EXTRA_CA_CERTS", "value": "/etc/boopd/e2e-ca/ca.crt"}},
			"extraVolumes":   []any{map[string]any{"name": "e2e-ca", "secret": map[string]any{"secretName": "e2e-ca"}}},
			"extraVolumeMounts": []any{
				map[string]any{"name": "e2e-ca", "mountPath": "/etc/boopd/e2e-ca", "readOnly": true},
			},
		}
	})
	hidden := h.seedUI(ctx, t)

	forwardFrom(ctx, t, h.env, "app.kubernetes.io/name=boopd-ui,app.kubernetes.io/instance=boopd", uiLocalPort, 3000)
	forwardFrom(ctx, t, h.env, "app=mock-oidc", issuerLocalPort, 9443)

	var purl string
	if err := h.db.QueryRow(ctx,
		"SELECT purl FROM dependencies WHERE repo_id = $1 AND purl IS NOT NULL ORDER BY purl LIMIT 1", h.repoID,
	).Scan(&purl); err != nil {
		t.Fatalf("a dependency's purl: %v", err)
	}
	issuerHost := "mock-oidc." + h.env.Namespace + ".svc"
	// The visual project compares screenshots rendered in the pinned
	// Playwright image; the stack run is the flows only.
	cmd := exec.CommandContext(ctx, "bun", "run", "e2e", "--project=chromium")
	cmd.Dir = filepath.Join("..", "..", "ui")
	cmd.Env = append(os.Environ(),
		"BOOP_UI_URL="+uiURL,
		fmt.Sprintf("BOOP_ISSUER_URL=https://%s:9443", issuerHost),
		"BOOP_E2E_REPO_ID="+strconv.FormatInt(h.repoID, 10),
		"BOOP_E2E_HIDDEN_REPO_ID="+strconv.FormatInt(hidden, 10),
		"BOOP_E2E_PURL="+purl,
		"BOOP_E2E_HOST_RULES=MAP "+issuerHost+" 127.0.0.1",
		"BOOP_E2E_IGNORE_HTTPS=1",
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bun run e2e: %v", err)
	}
}

// seedUI gives the pages something to show: a second installation whose
// one repository the viewer cannot see, the main repository's run, a bot
// pull request and a dashboard item. It returns the hidden repository's
// ID.
//
//nolint:contextcheck // Deliver uses t.Context
func (h *storeHarness) seedUI(ctx context.Context, t *testing.T) int64 {
	t.Helper()
	hidden := h.repoID + 2
	installations, err := json.Marshal([]fakegithub.Installation{
		{ID: 1, Account: ownerOf(runSlug)},
		{ID: 2, Account: hiddenOwner},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.stub(ctx, t, http.MethodPost, "_stub/installations", installations)
	repos := append([]fakegithub.Repo(nil), h.repos...)
	for i := range repos {
		repos[i].Installation = 1
	}
	repos = append(repos, fakegithub.Repo{
		ID: hidden, NodeID: "R_hidden", Slug: hiddenOwner + "/hidden", DefaultBranch: "main", Installation: 2,
		HasConfig: true, ConfigPath: ".github/renovate.json5", Config: `{extends: ["config:recommended",],}`,
	})
	h.setRepos(ctx, t, repos)
	h.onboard(ctx, t)
	eventually(t, 2*time.Minute, "the hidden repository in the store", func() bool {
		return h.count(ctx, t, "repositories", fmt.Sprintf("id = %d", hidden)) == 1
	})
	eventually(t, time.Minute, "the main repository's dependencies", func() bool {
		return h.count(ctx, t, "dependencies", fmt.Sprintf("repo_id = %d AND purl IS NOT NULL", h.repoID)) > 0
	})

	repo := h.repos[0]
	h.deliver(t, http.StatusAccepted, "pull_request", fakegithub.PullRequest("opened", &repo, 1, 12, e2eBot, e2eBot))
	box := dashboardBody("- [ ] <!-- approve-branch=renovate/react-19.x -->chore(deps): update react to v19 (`react`)")
	h.deliver(t, http.StatusAccepted, "issues", dashboardEdit(&repo, box, "", e2eBot))
	eventually(t, time.Minute, "the pull request and the waiting item", func() bool {
		return h.count(ctx, t, "pull_requests", fmt.Sprintf("repo_id = %d", repo.ID)) == 1 &&
			h.count(ctx, t, "dashboard_items", fmt.Sprintf("repo_id = %d", repo.ID)) == 1
	})
	return hidden
}

// ownerOf is a slug's owner.
func ownerOf(slug string) string {
	owner, _, _ := strings.Cut(slug, "/")
	return owner
}
