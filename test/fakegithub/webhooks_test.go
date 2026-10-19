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

package fakegithub_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/test/fakegithub"
)

// receiver records what Deliver sent and answers status.
type receiver struct {
	mu      sync.Mutex
	headers http.Header
	body    []byte
	status  int
}

func newReceiver(t *testing.T, status int) (*receiver, string) {
	t.Helper()
	rc := &receiver{status: status}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.headers, rc.body = r.Header.Clone(), b
		rc.mu.Unlock()
		w.WriteHeader(rc.status)
	}))
	t.Cleanup(srv.Close)
	return rc, srv.URL
}

func TestDeliver_SignsWithTheSecret(t *testing.T) {
	t.Parallel()
	gh := fakegithub.New(t)
	gh.SetWebhookSecret("s3cret")
	gh.SetAppID(42)
	rc, url := newReceiver(t, http.StatusAccepted)
	repo := fakegithub.Repo{ID: 7, Slug: "acme/app", DefaultBranch: "main"}

	d := gh.Deliver(t, url, "issues", fakegithub.IssuesEdited(&repo, 1, "boop-bot[bot]", "alice"))
	if d.StatusCode != http.StatusAccepted || d.Event != "issues" || d.Action != "edited" || d.RepositoryID != 7 || d.InstallationID != 1 {
		t.Errorf("delivery = %+v", d)
	}
	sig := rc.headers.Get("X-Hub-Signature-256")
	if _, err := github.ValidatePayloadFromBody("application/json", bytes.NewReader(rc.body), sig, []byte("s3cret")); err != nil {
		t.Errorf("signature does not verify with the secret: %v", err)
	}
	if _, err := github.ValidatePayloadFromBody("application/json", bytes.NewReader(rc.body), sig, []byte("other")); err == nil {
		t.Error("signature verifies with another secret")
	}
	for h, want := range map[string]string{
		"X-GitHub-Event": "issues", "X-GitHub-Delivery": d.GUID,
		"X-GitHub-Hook-Installation-Target-ID": "42", "X-GitHub-Hook-Installation-Target-Type": "integration",
	} {
		if got := rc.headers.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	if len(d.GUID) != 36 {
		t.Errorf("GUID %q is not a UUID", d.GUID)
	}
}

func TestDeliver_RedeliveryAndOptions(t *testing.T) {
	t.Parallel()
	gh := fakegithub.New(t)
	gh.SetWebhookSecret("s3cret")
	rc, url := newReceiver(t, http.StatusServiceUnavailable)

	first := gh.Deliver(t, url, "ping", fakegithub.Ping(1), fakegithub.WithSecret("wrong"))
	if first.StatusCode != http.StatusServiceUnavailable || first.Redelivery {
		t.Errorf("first = %+v", first)
	}
	if rc.headers.Get("X-Hub-Signature-256") != fakegithub.Sign("wrong", rc.body) {
		t.Error("WithSecret did not sign with the given secret")
	}
	rc.mu.Lock()
	rc.status = http.StatusOK
	rc.mu.Unlock()
	again := gh.Deliver(t, url, "ping", fakegithub.Ping(1), fakegithub.WithGUID(first.GUID))
	if !again.Redelivery || again.GUID != first.GUID || again.StatusCode != http.StatusOK {
		t.Errorf("redelivery = %+v", again)
	}

	lost := gh.RecordUndelivered(t, "push", "")
	if lost.StatusCode != 0 {
		t.Errorf("undelivered = %+v, want no status", lost)
	}
	closed := gh.Deliver(t, "http://127.0.0.1:1/", "push", map[string]any{})
	if closed.StatusCode != 0 {
		t.Errorf("delivery to a closed port = %+v, want status 0", closed)
	}
	if n := len(gh.Deliveries()); n != 4 {
		t.Errorf("%d deliveries recorded, want 4", n)
	}
}

// The hook-deliveries list pages newest first by cursor, as go-github
// reads GitHub's.
func TestHookDeliveries_CursorPaging(t *testing.T) {
	t.Parallel()
	gh := fakegithub.New(t)
	_, url := newReceiver(t, http.StatusAccepted)
	for range 5 {
		gh.Deliver(t, url, "ping", fakegithub.Ping(1))
	}
	c, err := github.NewClient(nil).WithEnterpriseURLs(gh.BaseURL(), gh.BaseURL())
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	opts := &github.ListCursorOptions{PerPage: 2}
	for {
		page, resp, err := c.Apps.ListHookDeliveries(t.Context(), opts)
		if err != nil {
			t.Fatalf("ListHookDeliveries: %v", err)
		}
		for _, d := range page {
			ids = append(ids, d.GetID())
			if d.GetStatusCode() != http.StatusAccepted || d.GetEvent() != "ping" || d.GetGUID() == "" {
				t.Errorf("delivery = %v", d)
			}
		}
		if resp.Cursor == "" {
			break
		}
		opts.Cursor = resp.Cursor
	}
	if got := strconv.Itoa(len(ids)); got != "5" || ids[0] != 5 || ids[4] != 1 {
		t.Errorf("ids = %v, want 5..1", ids)
	}
}

func TestDeleteInstallation(t *testing.T) {
	t.Parallel()
	gh := fakegithub.New(t)
	gh.SetInstallations(fakegithub.Installation{ID: 1, Account: "acme"}, fakegithub.Installation{ID: 2, Account: "other"})
	gh.DeleteInstallation(1)
	mint := func(id string) int {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			gh.URL+"/api/v3/app/installations/"+id+"/access_tokens", bytes.NewBufferString("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := mint("1"); got != http.StatusNotFound {
		t.Errorf("mint for the deleted installation = %d, want 404", got)
	}
	if got := mint("2"); got != http.StatusCreated {
		t.Errorf("mint for another installation = %d, want 201", got)
	}
	c, err := github.NewClient(nil).WithEnterpriseURLs(gh.BaseURL(), gh.BaseURL())
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := c.Apps.ListInstallations(t.Context(), nil)
	if err != nil || len(list) != 1 || list[0].GetID() != 2 {
		t.Errorf("installations = %v (err %v), want only 2", list, err)
	}
}

// Every builder decodes into go-github's typed event, the way ingest
// will read it.
func TestPayloads_ParseAsGitHubEvents(t *testing.T) {
	t.Parallel()
	repo := fakegithub.Repo{ID: 7, NodeID: "R_7", Slug: "acme/app", DefaultBranch: "main"}
	in := fakegithub.Installation{ID: 3, Account: "acme"}
	for name, c := range map[string]struct {
		event   string
		payload any
		check   func(any) bool
	}{
		"ping": {"ping", fakegithub.Ping(9), func(e any) bool { return e.(*github.PingEvent).GetZen() != "" }},
		"issues": {"issues", fakegithub.IssuesEdited(&repo, 3, "boop-bot[bot]", "alice"), func(e any) bool {
			ev := e.(*github.IssuesEvent)
			return ev.GetIssue().GetUser().GetLogin() == "boop-bot[bot]" && ev.GetSender().GetLogin() == "alice" &&
				ev.GetChanges().GetBody().GetFrom() != "" && ev.GetRepo().GetID() == 7
		}},
		"pull_request": {"pull_request", fakegithub.PullRequest("closed", &repo, 3, 4, "boop-bot[bot]", "alice"), func(e any) bool {
			ev := e.(*github.PullRequestEvent)
			return ev.GetAction() == "closed" && ev.GetPullRequest().GetUser().GetLogin() == "boop-bot[bot]"
		}},
		"push": {"push", fakegithub.Push(&repo, 3, "refs/heads/main", "alice", fakegithub.Commit{Added: []string{"renovate.json5"}}), func(e any) bool {
			ev := e.(*github.PushEvent)
			return ev.GetRef() == "refs/heads/main" && ev.Commits[0].Added[0] == "renovate.json5" && ev.GetRepo().GetDefaultBranch() == "main"
		}},
		"push at cap": {"push", fakegithub.PushAtCap(&repo, 3, "refs/heads/main", "alice"), func(e any) bool {
			return len(e.(*github.PushEvent).Commits) == fakegithub.PushCommitCap
		}},
		"repository": {"repository", fakegithub.RepositoryEvent("edited", &repo, 3, "alice"), func(e any) bool {
			return e.(*github.RepositoryEvent).GetChanges().GetDefaultBranch().GetFrom() == "master"
		}},
		"installation": {"installation", fakegithub.InstallationEvent("deleted", in, "alice", repo), func(e any) bool {
			ev := e.(*github.InstallationEvent)
			return ev.GetInstallation().GetID() == 3 && ev.Repositories[0].GetID() == 7
		}},
		"installation_repositories": {"installation_repositories", fakegithub.InstallationRepositories("removed", in, "alice", repo), func(e any) bool {
			ev := e.(*github.InstallationRepositoriesEvent)
			return len(ev.RepositoriesRemoved) == 1 && len(ev.RepositoriesAdded) == 0
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rc, url := newReceiver(t, http.StatusAccepted)
			gh := fakegithub.New(t)
			gh.Deliver(t, url, c.event, c.payload)
			ev, err := github.ParseWebHook(c.event, rc.body)
			if err != nil {
				t.Fatalf("ParseWebHook: %v", err)
			}
			if !c.check(ev) {
				t.Errorf("%T decoded without the routed fields: %s", ev, rc.body)
			}
		})
	}
}
