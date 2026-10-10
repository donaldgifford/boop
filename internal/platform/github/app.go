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

	ghinstallation "github.com/bradleyfalzon/ghinstallation/v2"
	gogithub "github.com/google/go-github/v62/github"
	"golang.org/x/time/rate"

	"github.com/donaldgifford/boop/internal/platform"
)

// installationsPageSize is the page size for GET /app/installations.
const installationsPageSize = 100

// AppClient authenticates as the GitHub App itself (a signed JWT), not as
// one installation. It lists installations and mints and revokes
// installation tokens (DESIGN-0001 § DiscoveryWorkflow, § RunRenovate
// activity). It is safe for concurrent use.
type AppClient struct {
	gh         *gogithub.Client
	httpClient *http.Client
	baseURL    string
	limiter    *rate.Limiter
}

// AppOption tunes the constructed AppClient.
type AppOption func(*AppClient)

// WithAppHTTPClient injects the base HTTP client; its transport carries
// the App JWT transport. Used in tests.
func WithAppHTTPClient(httpClient *http.Client) AppOption {
	return func(c *AppClient) { c.httpClient = httpClient }
}

// WithAppRateLimit overrides the client-side limiter.
func WithAppRateLimit(r rate.Limit, burst int) AppOption {
	return func(c *AppClient) { c.limiter = rate.NewLimiter(r, burst) }
}

// NewAppClient builds an AppClient from the App id and its PEM private
// key. endpoint is the API base URL; empty means api.github.com.
func NewAppClient(appID int64, key []byte, endpoint string, opts ...AppOption) (*AppClient, error) {
	if appID == 0 {
		return nil, errors.New("github: app id required")
	}
	if len(key) == 0 {
		return nil, errors.New("github: app private key required")
	}

	c := &AppClient{
		baseURL: endpoint,
		limiter: rate.NewLimiter(defaultRateLimit, defaultRateBurst),
	}
	for _, opt := range opts {
		opt(c)
	}

	transport := http.DefaultTransport
	if c.httpClient != nil && c.httpClient.Transport != nil {
		transport = c.httpClient.Transport
	}
	atr, err := ghinstallation.NewAppsTransport(transport, appID, key)
	if err != nil {
		return nil, fmt.Errorf("github: app transport: %w", err)
	}
	if c.baseURL != "" && !isPublicGitHub(c.baseURL) {
		atr.BaseURL = c.baseURL
	}

	gh, err := buildGoGitHubClient(&http.Client{Transport: atr}, c.baseURL)
	if err != nil {
		return nil, err
	}
	c.gh = gh
	return c, nil
}

// ListInstallations pages GET /app/installations, 100 per page, and
// returns every installation of the App. Filtering by the configured
// allowlist and dropping suspended installations is the caller's job.
func (c *AppClient) ListInstallations(ctx context.Context) ([]platform.Installation, error) {
	opt := &gogithub.ListOptions{PerPage: installationsPageSize}
	var all []platform.Installation
	for {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		page, resp, err := c.gh.Apps.ListInstallations(ctx, opt)
		if err != nil {
			return nil, classifyErr(resp, err)
		}
		for _, in := range page {
			all = append(all, toInstallation(in))
		}
		if resp.NextPage == 0 {
			return all, nil
		}
		opt.Page = resp.NextPage
	}
}

func toInstallation(in *gogithub.Installation) platform.Installation {
	return platform.Installation{
		ID:                  in.GetID(),
		Account:             in.GetAccount().GetLogin(),
		SuspendedAt:         in.GetSuspendedAt().Time,
		RepositorySelection: in.GetRepositorySelection(),
	}
}
