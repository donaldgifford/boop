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

// CheckRepo answers whether a repository discovery has stopped seeing is
// really gone (DESIGN-0001 § RepoWorkflow): GET /repositories/{id} with
// the installation token, then the config file on its default branch.
//
//   - 404 on the repository, or archived: RepoGone.
//   - Present without the configured file: RepoNoConfig.
//   - Present with it: RepoPresent, with the current metadata so the
//     caller can pick up a rename or a new default branch.
//   - Anything else (5xx, 401/403, rate limits, network): an error and
//     RepoUnknown. The caller keeps waiting; it never offboards on one.
//
// GitHub answers /repositories/{id} for any public repository, so a
// public repository removed from a "selected" installation reads as
// present here; its next run's scoped mint fails instead.
func (c *Client) CheckRepo(ctx context.Context, repoID int64) (platform.RepoState, *platform.Repository, error) {
	if repoID <= 0 {
		return platform.RepoUnknown, nil, errors.New("github: repository id required")
	}
	if err := c.wait(ctx); err != nil {
		return platform.RepoUnknown, nil, err
	}
	r, resp, err := c.gh.Repositories.GetByID(ctx, repoID)
	if err != nil {
		classified := classifyErr(resp, err)
		if errors.Is(classified, platform.ErrNotFound) {
			return platform.RepoGone, nil, nil
		}
		return platform.RepoUnknown, nil, classified
	}
	repo := toRepo(r)
	if repo.Archived {
		return platform.RepoGone, &repo, nil
	}
	ok, err := c.HasRenovateConfig(ctx, &repo)
	if err != nil {
		return platform.RepoUnknown, nil, err
	}
	if !ok {
		return platform.RepoNoConfig, &repo, nil
	}
	return platform.RepoPresent, &repo, nil
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
