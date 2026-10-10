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
	"path"
	"slices"

	gogithub "github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/internal/platform"
)

const discoverPageSize = 100

// Discover lists every repo that survives the supplied filters.
//
// App-auth Clients page /installation/repositories (DiscoverPages), the
// installation-scoped endpoint that returns exactly what the App was
// granted (public + private). The public /users/{owner}/repos fallback
// would silently leak every public repo for the owner regardless of which
// repos the installation actually authorized. See INV-0004. filter.Owner
// is optional there; when set it narrows the result defensively.
//
// PAT-auth Clients have no installation concept and keep the
// /orgs/{owner}/repos → /users/{owner}/repos fallback; Owner is required.
func (c *Client) Discover(ctx context.Context, filter platform.DiscoveryFilter) ([]platform.Repository, error) {
	if c.appTransport != nil {
		var out []platform.Repository
		err := c.DiscoverPages(ctx, filter, func(p Page) error {
			out = append(out, p.Repos...)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	}

	if filter.Owner == "" {
		return nil, fmt.Errorf("github: DiscoveryFilter.Owner required")
	}
	repos, err := c.listOrgOrUserRepos(ctx, filter.Owner)
	if err != nil {
		return nil, err
	}
	out := make([]platform.Repository, 0, len(repos))
	for _, r := range repos {
		if matchesFilter(r, filter) {
			out = append(out, toRepo(r))
		}
	}
	return out, nil
}

// Page is one page of installation discovery.
type Page struct {
	// Number is the 1-based page number, for the activity's heartbeat.
	Number int
	// Seen is how many repositories the page listed before filtering.
	Seen int
	// Repos are the page's repositories that survived the filter.
	Repos []platform.Repository
}

// DiscoverPages pages GET /installation/repositories, 100 per page, and
// calls fn once per page with the repositories that survive filter
// (skipForks, skipArchived, and topics, patterns and owner when set).
// fn returning an error stops paging and DiscoverPages returns that
// error. Paging is exposed so DiscoverInstallation can probe and signal
// each page and heartbeat its number without holding the whole list
// (DESIGN-0001 § DiscoveryWorkflow). Only App-auth Clients can call it.
func (c *Client) DiscoverPages(ctx context.Context, filter platform.DiscoveryFilter, fn func(Page) error) error {
	if c.appTransport == nil {
		return errors.New("github: DiscoverPages needs an App-auth client")
	}
	opt := &gogithub.ListOptions{PerPage: discoverPageSize}
	for number := 1; ; number++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		page, resp, err := c.gh.Apps.ListRepos(ctx, opt)
		if err != nil {
			return classifyErr(resp, err)
		}
		p := Page{Number: number, Seen: len(page.Repositories)}
		for _, r := range page.Repositories {
			if filter.Owner != "" && r.GetOwner().GetLogin() != filter.Owner {
				continue
			}
			if matchesFilter(r, filter) {
				p.Repos = append(p.Repos, toRepo(r))
			}
		}
		if err := fn(p); err != nil {
			return err
		}
		if resp.NextPage == 0 {
			return nil
		}
		opt.Page = resp.NextPage
	}
}

// listOrgOrUserRepos is the PAT-auth path. /orgs/{owner}/repos 404s for
// personal accounts; fall back to /users/{user}/repos.
func (c *Client) listOrgOrUserRepos(ctx context.Context, owner string) ([]*gogithub.Repository, error) {
	repos, err := c.listOrgRepos(ctx, owner)
	if err != nil {
		// errors.Is matches platform.ErrNotFound via our classifyErr wrapper.
		if !isNotFound(err) {
			return nil, err
		}
		return c.listUserRepos(ctx, owner)
	}
	return repos, nil
}

func (c *Client) listOrgRepos(ctx context.Context, owner string) ([]*gogithub.Repository, error) {
	opt := &gogithub.RepositoryListByOrgOptions{PerPage: discoverPageSize}
	var all []*gogithub.Repository
	for {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		page, resp, err := c.gh.Repositories.ListByOrg(ctx, owner, opt)
		if err != nil {
			return nil, classifyErr(resp, err)
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}

func (c *Client) listUserRepos(ctx context.Context, owner string) ([]*gogithub.Repository, error) {
	opt := &gogithub.RepositoryListByUserOptions{PerPage: discoverPageSize}
	var all []*gogithub.Repository
	for {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		page, resp, err := c.gh.Repositories.ListByUser(ctx, owner, opt)
		if err != nil {
			return nil, classifyErr(resp, err)
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}

func matchesFilter(r *gogithub.Repository, filter platform.DiscoveryFilter) bool {
	if filter.SkipArchived && r.GetArchived() {
		return false
	}
	if filter.SkipForks && r.GetFork() {
		return false
	}
	if len(filter.Topics) > 0 && !anyTopicMatches(r.Topics, filter.Topics) {
		return false
	}
	if len(filter.Patterns) > 0 && !anyPatternMatches(r.GetFullName(), filter.Patterns) {
		return false
	}
	return true
}

func anyTopicMatches(have, want []string) bool {
	for _, t := range want {
		if slices.Contains(have, t) {
			return true
		}
	}
	return false
}

// anyPatternMatches returns true when fullName matches any glob in patterns.
// Renovate's autodiscover globs use fnmatch; path.Match handles the same
// "owner/*" / "owner/prefix-*" shapes for our purposes.
func anyPatternMatches(fullName string, patterns []string) bool {
	for _, p := range patterns {
		ok, err := path.Match(p, fullName)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func toRepo(r *gogithub.Repository) platform.Repository {
	return platform.Repository{
		ID:            r.GetID(),
		NodeID:        r.GetNodeID(),
		Slug:          r.GetFullName(),
		DefaultBranch: r.GetDefaultBranch(),
		Archived:      r.GetArchived(),
		Fork:          r.GetFork(),
		Topics:        append([]string(nil), r.Topics...),
	}
}
