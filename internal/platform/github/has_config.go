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
	"strings"

	"github.com/donaldgifford/boop/internal/platform"
)

// HasRenovateConfig reports whether the repository carries any of the
// configured config files (WithConfigPaths, default
// platform.DefaultConfigPaths) on its default branch. It lists the
// directories the paths live in (locateConfig); any error other than a
// 404 is classified.
func (c *Client) HasRenovateConfig(ctx context.Context, repo *platform.Repository) (bool, error) {
	found, _, err := c.locateConfig(ctx, repo, c.paths())
	return found != "", err
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
