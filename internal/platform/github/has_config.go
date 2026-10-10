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
	"strings"

	gogithub "github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/internal/platform"
)

// HasRenovateConfig reports whether the repository carries the configured
// config file (WithConfigPath, default platform.DefaultConfigPath) on its
// default branch. A 404 is false; any other error is classified.
func (c *Client) HasRenovateConfig(ctx context.Context, repo *platform.Repository) (bool, error) {
	owner, name, ok := splitSlug(repo.Slug)
	if !ok {
		return false, fmt.Errorf("github: invalid slug %q", repo.Slug)
	}
	if err := c.wait(ctx); err != nil {
		return false, err
	}
	opt := &gogithub.RepositoryContentGetOptions{Ref: repo.DefaultBranch}
	_, _, resp, err := c.gh.Repositories.GetContents(ctx, owner, name, c.path(), opt)
	if err == nil {
		return true, nil
	}
	classified := classifyErr(resp, err)
	if errors.Is(classified, platform.ErrNotFound) {
		return false, nil
	}
	return false, classified
}

func splitSlug(slug string) (string, string, bool) {
	idx := strings.IndexByte(slug, '/')
	if idx <= 0 || idx == len(slug)-1 {
		return "", "", false
	}
	return slug[:idx], slug[idx+1:], true
}

// isNotFound is the small helper Discover uses to detect a 404 on the org
// list endpoint so it can fall back to /users/{user}/repos.
func isNotFound(err error) bool {
	return errors.Is(err, platform.ErrNotFound)
}
