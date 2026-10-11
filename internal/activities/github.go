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

package activities

import (
	"context"
	"fmt"
	"sync"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/platform/github"
	"github.com/donaldgifford/boop/internal/workflows"
)

// AppAPI is what the activities need from the App-level client.
type AppAPI interface {
	ListInstallations(ctx context.Context) ([]platform.Installation, error)
	platform.Minter
}

// InstallationAPI is what the activities need from an installation's
// client.
type InstallationAPI interface {
	DiscoverPages(ctx context.Context, filter platform.DiscoveryFilter, fn func(github.Page) error) error
	ProbeConfig(ctx context.Context, nodeIDs []string, path string) (*github.ProbeResult, error)
	ProbeConfigREST(ctx context.Context, repos []platform.Repository, path string) (*github.ProbeResult, error)
	ReadRateLimit(ctx context.Context) (*workflows.Readings, error)
	CheckRepo(ctx context.Context, repoID int64) (platform.RepoState, *platform.Repository, error)
	RepoIDBySlug(ctx context.Context, slug string) (int64, error)
	SetDiscoveredLimit(limit int)
}

// RateReader reads /rate_limit with a run's own token.
type RateReader interface {
	ReadRateLimit(ctx context.Context) (*workflows.Readings, error)
}

// GitHub builds the clients the activities use.
type GitHub interface {
	App(app *config.App) (AppAPI, error)
	Installation(app *config.App, installationID int64, configPath string) (InstallationAPI, error)
	Token(app *config.App, token string) (RateReader, error)
}

// NewGitHub returns the GitHub clients over the real API. App and
// installation clients are cached: an installation client keeps its
// token until it nears expiry, so activities do not mint one per call.
func NewGitHub() GitHub {
	return &ghClients{apps: make(map[string]*github.AppClient), installs: make(map[int64]*github.Client)}
}

type ghClients struct {
	mu       sync.Mutex
	apps     map[string]*github.AppClient
	installs map[int64]*github.Client
}

func (g *ghClients) App(app *config.App) (AppAPI, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.apps[app.Name]; ok {
		return c, nil
	}
	c, err := github.NewAppClient(app.AppID, app.PrivateKey.Bytes(), baseURL(app))
	if err != nil {
		return nil, fmt.Errorf("app %s: %w", app.Name, err)
	}
	g.apps[app.Name] = c
	return c, nil
}

func (g *ghClients) Installation(app *config.App, installationID int64, configPath string) (InstallationAPI, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.installs[installationID]; ok {
		return c, nil
	}
	c, err := github.NewWithApp(github.AppAuth{
		AppID:          app.AppID,
		InstallationID: installationID,
		PEM:            app.PrivateKey.Bytes(),
		BaseURL:        baseURL(app),
	}, github.WithConfigPath(configPath))
	if err != nil {
		return nil, fmt.Errorf("installation %d: %w", installationID, err)
	}
	g.installs[installationID] = c
	return c, nil
}

func (*ghClients) Token(app *config.App, token string) (RateReader, error) {
	c, err := github.NewWithToken(github.TokenAuth{Token: token, BaseURL: baseURL(app)})
	if err != nil {
		return nil, fmt.Errorf("token client: %w", err)
	}
	return c, nil
}

// baseURL is the app's endpoint with the trailing slash go-github wants.
func baseURL(app *config.App) string {
	if app.Endpoint == "" || app.Endpoint[len(app.Endpoint)-1] == '/' {
		return app.Endpoint
	}
	return app.Endpoint + "/"
}
