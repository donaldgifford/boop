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
	"net/http"
	"net/url"

	gogithub "github.com/google/go-github/v62/github"
)

// Tree is a repository's file listing at one ref.
type Tree struct {
	// Paths are the files (blobs), relative to the repository root.
	Paths []string
	// Truncated is true when GitHub cut the listing short; Paths is then
	// incomplete.
	Truncated bool
}

// ListTree lists the files at ref with one call,
// GET /repositories/{id}/git/trees/{ref}?recursive=1 (DESIGN-0005
// § Learning the managers safely). The repository is addressed by id,
// so a rename between discovery and the run does not matter.
func (c *Client) ListTree(ctx context.Context, repoID int64, ref string) (*Tree, error) {
	if repoID <= 0 || ref == "" {
		return nil, errors.New("github: list tree needs a repository id and a ref")
	}
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	u := fmt.Sprintf("repositories/%d/git/trees/%s?recursive=1", repoID, url.PathEscape(ref))
	req, err := c.gh.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("github: list tree: %w", err)
	}
	var tree gogithub.Tree
	resp, err := c.gh.Do(ctx, req, &tree)
	if err != nil {
		return nil, fmt.Errorf("github: list tree of %d at %s: %w", repoID, ref, classifyErr(resp, err))
	}
	out := &Tree{Truncated: tree.GetTruncated()}
	for _, e := range tree.Entries {
		if e.GetType() == "blob" {
			out.Paths = append(out.Paths, e.GetPath())
		}
	}
	return out, nil
}
