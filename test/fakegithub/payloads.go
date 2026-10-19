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

package fakegithub

import (
	"strings"
)

// Payload builders for the webhook events ingest routes (DESIGN-0003 §
// Routing table), trimmed to the fields it reads. Each returns a value
// for Deliver.

// JSON keys the builders repeat.
const (
	keyLogin = "login"
	keyName  = "name"
	keyFrom  = "from"
	keyType  = "type"
)

// PushCommitCap is how many commits GitHub lists in a push payload at
// most; a push at the cap may hide more.
const PushCommitCap = 2048

// Commit is one push commit's file lists.
type Commit struct {
	Added, Modified, Removed []string
}

// user is a sender or author; a login ending in [bot] is a Bot.
func user(login string) map[string]any {
	typ := "User"
	if strings.HasSuffix(login, "[bot]") {
		typ = "Bot"
	}
	return map[string]any{keyLogin: login, keyType: typ}
}

func base(action string, repo *Repo, installationID int64, sender string) map[string]any {
	p := map[string]any{"installation": map[string]any{"id": installationID}, "sender": user(sender)}
	if action != "" {
		p["action"] = action
	}
	if repo != nil {
		p["repository"] = repoJSON(repo)
	}
	return p
}

// Ping is the event GitHub sends when the webhook is set up.
func Ping(appID int64) map[string]any {
	return map[string]any{
		"zen": "Design for failure.", "hook_id": 1,
		"hook": map[string]any{keyType: "App", "app_id": appID, "events": []string{"issues", "pull_request", "push", "repository"}},
	}
}

// IssuesEdited is an issues.edited whose body went from an unticked to a
// ticked box, as on a Dependency Dashboard.
func IssuesEdited(repo *Repo, installationID int64, author, sender string) map[string]any {
	p := base("edited", repo, installationID, sender)
	p["issue"] = map[string]any{"number": 1, "title": "Dependency Dashboard", "user": user(author), "body": "- [x] <!-- approve-all -->"}
	p["changes"] = map[string]any{"body": map[string]any{keyFrom: "- [ ] <!-- approve-all -->"}}
	return p
}

// PullRequest is a pull_request event (edited, closed, ...) on PR number.
func PullRequest(action string, repo *Repo, installationID int64, number int, author, sender string) map[string]any {
	p := base(action, repo, installationID, sender)
	p["number"] = number
	state := "open"
	if action == "closed" {
		state = "closed"
	}
	p["pull_request"] = map[string]any{
		"number": number, "user": user(author), "state": state,
		"head": map[string]any{"ref": "renovate/all"}, "base": map[string]any{"ref": repo.DefaultBranch},
	}
	return p
}

// Push is a push to ref with commits' file lists.
func Push(repo *Repo, installationID int64, ref, sender string, commits ...Commit) map[string]any {
	p := base("", repo, installationID, sender)
	list := make([]map[string]any, 0, len(commits))
	for _, c := range commits {
		list = append(list, map[string]any{
			"id": "0000000000000000000000000000000000000000", "added": nonNil(c.Added),
			"modified": nonNil(c.Modified), "removed": nonNil(c.Removed),
		})
	}
	p["ref"] = ref
	p["commits"] = list
	return p
}

// PushAtCap is a push to ref listing PushCommitCap commits that touch no
// config path.
func PushAtCap(repo *Repo, installationID int64, ref, sender string) map[string]any {
	commits := make([]Commit, PushCommitCap)
	for i := range commits {
		commits[i] = Commit{Modified: []string{"main.go"}}
	}
	return Push(repo, installationID, ref, sender, commits...)
}

// RepositoryEvent is a repository event; edited carries a default branch
// change from "master", renamed a name change.
func RepositoryEvent(action string, repo *Repo, installationID int64, sender string) map[string]any {
	p := base(action, repo, installationID, sender)
	switch action {
	case "edited":
		p["changes"] = map[string]any{"default_branch": map[string]any{keyFrom: "master"}}
	case "renamed":
		p["changes"] = map[string]any{"repository": map[string]any{keyName: map[string]any{keyFrom: "old-name"}}}
	}
	return p
}

// InstallationEvent is an installation event (created, deleted, suspend,
// unsuspend, new_permissions_accepted) listing repos.
func InstallationEvent(action string, in Installation, sender string, repos ...Repo) map[string]any {
	p := base(action, nil, in.ID, sender)
	p["installation"] = map[string]any{"id": in.ID, "account": map[string]any{keyLogin: in.Account}}
	p["repositories"] = shortRepos(repos)
	return p
}

// InstallationRepositories is an installation_repositories event: added
// lists repos under repositories_added, removed under
// repositories_removed.
func InstallationRepositories(action string, in Installation, sender string, repos ...Repo) map[string]any {
	p := base(action, nil, in.ID, sender)
	p["repository_selection"] = "selected"
	key, other := "repositories_added", "repositories_removed"
	if action == "removed" {
		key, other = other, key
	}
	p[key] = shortRepos(repos)
	p[other] = []map[string]any{}
	return p
}

// shortRepos is the repository list installation events carry.
func shortRepos(repos []Repo) []map[string]any {
	out := make([]map[string]any, 0, len(repos))
	for i := range repos {
		_, name, _ := strings.Cut(repos[i].Slug, "/")
		out = append(out, map[string]any{"id": repos[i].ID, "node_id": repos[i].NodeID, "full_name": repos[i].Slug, keyName: name})
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
