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

package github

import (
	"context"
	"errors"
	"fmt"

	"github.com/donaldgifford/boop/internal/platform"
)

// RepoCheck is CheckRepo's answer.
type RepoCheck struct {
	State platform.RepoState
	// Repo is the repository's current metadata; nil when GitHub
	// answered 404.
	Repo *platform.Repository
	// Config is the config probe of a present repository: the file
	// found and its extends (ParseErr when it does not parse).
	Config ConfigProbe
}

// CheckRepo answers whether a repository is still onboarded
// (DESIGN-0001 § RepoWorkflow, DESIGN-0003 § CheckRepo returns extends):
// GET /repositories/{id} with the installation token, then the
// one-repository REST config probe over the client's config paths on
// its default branch.
//
//   - 404 on the repository, or archived: RepoGone.
//   - The installation token's mint answered 404: an error wrapping
//     platform.ErrInstallationGone, which the caller maps to gone.
//   - Present without a config file: RepoNoConfig.
//   - Present with one: RepoPresent, with the current metadata so the
//     caller can pick up a rename or a new default branch, and the
//     probe's extends.
//   - Anything else (5xx, 401/403, rate limits, network): an error and
//     RepoUnknown. The caller keeps waiting; it never offboards on one.
//
// GitHub answers /repositories/{id} for any public repository, so a
// public repository removed from a "selected" installation reads as
// present here; its next run's scoped mint fails instead.
func (c *Client) CheckRepo(ctx context.Context, repoID int64) (*RepoCheck, error) {
	unknown := &RepoCheck{State: platform.RepoUnknown}
	if repoID <= 0 {
		return unknown, errors.New("github: repository id required")
	}
	if err := c.wait(ctx); err != nil {
		return unknown, err
	}
	r, resp, err := c.gh.Repositories.GetByID(ctx, repoID)
	if err != nil {
		classified := classifyErr(resp, err)
		if errors.Is(classified, platform.ErrNotFound) {
			return &RepoCheck{State: platform.RepoGone}, nil
		}
		return unknown, classified
	}
	repo := toRepo(r)
	if repo.Archived {
		return &RepoCheck{State: platform.RepoGone, Repo: &repo}, nil
	}
	probe, err := c.ProbeConfigREST(ctx, []platform.Repository{repo}, nil)
	if err != nil {
		return unknown, err
	}
	cfg := probe.Configs[repo.NodeID]
	if !cfg.Exists {
		return &RepoCheck{State: platform.RepoNoConfig, Repo: &repo}, nil
	}
	return &RepoCheck{State: platform.RepoPresent, Repo: &repo, Config: cfg}, nil
}

// RepoIDBySlug returns the numeric id of owner/name, for the shared-preset
// repository a run's token is scoped to (DESIGN-0001 OQ2).
func (c *Client) RepoIDBySlug(ctx context.Context, slug string) (int64, error) {
	owner, name, ok := splitSlug(slug)
	if !ok {
		return 0, fmt.Errorf("github: invalid slug %q", slug)
	}
	if err := c.wait(ctx); err != nil {
		return 0, err
	}
	r, resp, err := c.gh.Repositories.Get(ctx, owner, name)
	if err != nil {
		return 0, classifyErr(resp, err)
	}
	return r.GetID(), nil
}
