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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	gogithub "github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/internal/platform"
)

const (
	// probeBatch is how many node ids one GraphQL probe carries. It is a
	// design choice, not a documented limit (DESIGN-0001 § DiscoveryWorkflow).
	probeBatch = 100

	// maxConfigBytes bounds the config text parsed for extends; a larger
	// file yields an empty list.
	maxConfigBytes = 64 << 10
)

// probeQuery is the config probe. The expression is a variable, so the
// configured path is never spliced into the query text.
const probeQuery = `query($ids: [ID!]!, $expr: String!) {
  rateLimit { cost remaining resetAt }
  nodes(ids: $ids) {
    ... on Repository {
      id
      config: object(expression: $expr) {
        ... on Blob { byteSize text }
      }
    }
  }
}`

// ConfigProbe is one repository's probe answer.
type ConfigProbe struct {
	// Exists is true when the config file is on the default branch.
	Exists bool
	// Extends is the file's extends list; empty when the file is absent,
	// above 64 KB or does not parse as JSON.
	Extends []string
}

// ProbeResult is the outcome of probing a set of repositories.
type ProbeResult struct {
	// Configs holds one answer per requested node id. A node the
	// installation cannot see, or that is not a repository, has
	// Exists false.
	Configs map[string]ConfigProbe
	// Queries is how many requests the probe sent.
	Queries int
	// Cost is the sum of rateLimit.cost over the GraphQL queries; the
	// pass records it (boopd_discovery_probe_cost). Zero for REST.
	Cost int
	// Remaining and ResetAt are the GraphQL budget after the last query.
	Remaining int
	ResetAt   time.Time
}

type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type probeResponse struct {
	Data *struct {
		RateLimit struct {
			Cost      int       `json:"cost"`
			Remaining int       `json:"remaining"`
			ResetAt   time.Time `json:"resetAt"`
		} `json:"rateLimit"`
		Nodes []*struct {
			ID     string `json:"id"`
			Config *struct {
				ByteSize int     `json:"byteSize"`
				Text     *string `json:"text"`
			} `json:"config"`
		} `json:"nodes"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// ProbeConfig asks GitHub's GraphQL API whether each repository carries
// the config file at path on its default branch (HEAD), one query per 100
// node ids, and parses extends from the file's text (DESIGN-0001 OQ3).
// Every query's rateLimit.cost is read and summed into the result.
func (c *Client) ProbeConfig(ctx context.Context, nodeIDs []string, path string) (*ProbeResult, error) {
	if path == "" {
		path = c.path()
	}
	res := &ProbeResult{Configs: make(map[string]ConfigProbe, len(nodeIDs))}
	for _, id := range nodeIDs {
		res.Configs[id] = ConfigProbe{}
	}
	for start := 0; start < len(nodeIDs); start += probeBatch {
		batch := nodeIDs[start:min(start+probeBatch, len(nodeIDs))]
		if err := c.probeBatch(ctx, batch, path, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (c *Client) probeBatch(ctx context.Context, ids []string, path string, res *ProbeResult) error {
	if err := c.wait(ctx); err != nil {
		return err
	}
	req, err := c.gh.NewRequest(http.MethodPost, c.graphqlURL(), &graphqlRequest{
		Query:     probeQuery,
		Variables: map[string]any{"ids": ids, "expr": "HEAD:" + path},
	})
	if err != nil {
		return fmt.Errorf("github: build probe request: %w", err)
	}
	var out probeResponse
	resp, err := c.gh.Do(ctx, req, &out)
	res.Queries++
	if err != nil {
		return classifyErr(resp, err)
	}
	if out.Data == nil {
		msgs := make([]string, 0, len(out.Errors))
		for _, e := range out.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("%w: github: probe returned no data: %s", platform.ErrTransient, strings.Join(msgs, "; "))
	}
	res.Cost += out.Data.RateLimit.Cost
	res.Remaining = out.Data.RateLimit.Remaining
	res.ResetAt = out.Data.RateLimit.ResetAt
	for _, n := range out.Data.Nodes {
		if n == nil || n.ID == "" || n.Config == nil {
			continue
		}
		if _, asked := res.Configs[n.ID]; !asked {
			continue
		}
		text := ""
		if n.Config.Text != nil {
			text = *n.Config.Text
		}
		res.Configs[n.ID] = ConfigProbe{Exists: true, Extends: parseExtends(text, n.Config.ByteSize)}
	}
	return nil
}

// ProbeConfigREST is the fallback behind discovery.probe: rest: one
// GET /repos/{slug}/contents/{path} per repository on its default
// branch. Answers are keyed by NodeID, as ProbeConfig's are.
func (c *Client) ProbeConfigREST(ctx context.Context, repos []platform.Repository, path string) (*ProbeResult, error) {
	if path == "" {
		path = c.path()
	}
	res := &ProbeResult{Configs: make(map[string]ConfigProbe, len(repos))}
	for i := range repos {
		probe, err := c.probeREST(ctx, &repos[i], path)
		res.Queries++
		if err != nil {
			return nil, err
		}
		res.Configs[repos[i].NodeID] = probe
	}
	return res, nil
}

func (c *Client) probeREST(ctx context.Context, repo *platform.Repository, path string) (ConfigProbe, error) {
	owner, name, ok := splitSlug(repo.Slug)
	if !ok {
		return ConfigProbe{}, fmt.Errorf("github: invalid slug %q", repo.Slug)
	}
	if err := c.wait(ctx); err != nil {
		return ConfigProbe{}, err
	}
	opt := &gogithub.RepositoryContentGetOptions{Ref: repo.DefaultBranch}
	file, _, resp, err := c.gh.Repositories.GetContents(ctx, owner, name, path, opt)
	if err != nil {
		classified := classifyErr(resp, err)
		if errors.Is(classified, platform.ErrNotFound) {
			return ConfigProbe{}, nil
		}
		return ConfigProbe{}, classified
	}
	if file == nil {
		// path names a directory, not a file.
		return ConfigProbe{}, nil
	}
	text, err := file.GetContent()
	if err != nil {
		// Undecodable content still counts as present, with no extends.
		text = ""
	}
	return ConfigProbe{Exists: true, Extends: parseExtends(text, file.GetSize())}, nil
}

// parseExtends returns the config's extends list. Renovate accepts a
// string or an array of strings. Files above 64 KB, text that is not
// JSON and any other extends shape yield nil.
func parseExtends(text string, byteSize int) []string {
	if byteSize > maxConfigBytes || len(text) > maxConfigBytes || text == "" {
		return nil
	}
	var cfg struct {
		Extends json.RawMessage `json:"extends"`
	}
	if err := json.Unmarshal([]byte(text), &cfg); err != nil || len(cfg.Extends) == 0 {
		return nil
	}
	var list []string
	if err := json.Unmarshal(cfg.Extends, &list); err == nil {
		return list
	}
	var one string
	if err := json.Unmarshal(cfg.Extends, &one); err == nil && one != "" {
		return []string{one}
	}
	return nil
}

// graphqlURL is the GraphQL endpoint for the client's base URL:
// api.github.com/graphql on github.com, <host>/api/graphql on GHES.
func (c *Client) graphqlURL() string {
	base := c.gh.BaseURL
	if p, ok := strings.CutSuffix(base.Path, "/api/v3/"); ok {
		u := *base
		u.Path = p + "/api/graphql"
		return u.String()
	}
	return base.ResolveReference(&url.URL{Path: "graphql"}).String()
}
