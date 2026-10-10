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
	"time"

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

	// transport is the base transport, without the JWT, that Revoke wraps
	// with the token being revoked.
	transport http.RoundTripper
}

var _ platform.Minter = (*AppClient)(nil)

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
	c.transport = transport
	return c, nil
}

// Mint creates an installation token with
// POST /app/installations/{id}/access_tokens, restricted to repoIDs
// (the run's repository and the shared-preset repository, OQ2). An empty
// repoIDs is refused: an unscoped token is never what a run wants.
func (c *AppClient) Mint(ctx context.Context, installationID int64, repoIDs []int64) (string, time.Time, error) {
	if installationID == 0 {
		return "", time.Time{}, errors.New("github: installation id required")
	}
	if len(repoIDs) == 0 {
		return "", time.Time{}, errors.New("github: mint needs at least one repository id")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return "", time.Time{}, err
	}
	tok, resp, err := c.gh.Apps.CreateInstallationToken(ctx, installationID, &gogithub.InstallationTokenOptions{
		RepositoryIDs: repoIDs,
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: mint installation token: %w", classifyErr(resp, err))
	}
	if tok.GetToken() == "" {
		return "", time.Time{}, errors.New("github: mint returned an empty token")
	}
	return tok.GetToken(), tok.GetExpiresAt().Time, nil
}

// Revoke ends token with DELETE /installation/token, authenticated as
// the token itself.
func (c *AppClient) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("github: revoke needs a token")
	}
	gh, err := buildGoGitHubClient(&http.Client{Transport: &tokenTransport{token: token, base: c.transport}}, c.baseURL)
	if err != nil {
		return err
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	resp, err := gh.Apps.RevokeInstallationToken(ctx)
	if err != nil {
		return fmt.Errorf("github: revoke installation token: %w", classifyErr(resp, err))
	}
	return nil
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
