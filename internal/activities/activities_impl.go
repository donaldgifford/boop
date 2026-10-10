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
	"log/slog"
	"slices"
	"sync"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/workflows"
)

// Deps are what the activities need. Config must have had ReadSecrets
// called.
type Deps struct {
	Config    *config.Config
	GitHub    GitHub
	Temporal  client.Client
	TaskQueue string
	Runner    *kube.Runner
	Metrics   Metrics
	Logger    *slog.Logger
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Activities holds every boopd activity (DESIGN-0001 § Topology and
// packages). It is safe for concurrent use.
type Activities struct {
	cfg       *config.Config
	gh        GitHub
	temporal  client.Client
	taskQueue string
	runner    *kube.Runner
	metrics   Metrics
	log       *slog.Logger
	now       func() time.Time

	mu       sync.Mutex
	appOf    map[int64]string // installation id -> app name, learned from listings
	presetID map[string]int64 // app name -> shared-preset repository id
}

// New builds the activities from deps.
func New(deps *Deps) *Activities {
	a := &Activities{
		cfg:       deps.Config,
		gh:        deps.GitHub,
		temporal:  deps.Temporal,
		taskQueue: deps.TaskQueue,
		runner:    deps.Runner,
		metrics:   deps.Metrics,
		log:       deps.Logger,
		now:       deps.Now,
		appOf:     make(map[int64]string),
		presetID:  make(map[string]int64),
	}
	if a.gh == nil {
		a.gh = NewGitHub()
	}
	if a.metrics == nil {
		a.metrics = NopMetrics{}
	}
	if a.log == nil {
		a.log = slog.Default()
	}
	if a.now == nil {
		a.now = time.Now
	}
	return a
}

// app returns the configured app by name.
func (a *Activities) app(name string) (*config.App, error) {
	if app := a.cfg.App(name); app != nil {
		return app, nil
	}
	return nil, fmt.Errorf("no app %q in config", name)
}

// appFor returns the app an installation belongs to: the only app when
// one is configured, else the one whose listing included it. A worker
// that has not run discovery yet lists every app once to learn.
func (a *Activities) appFor(ctx context.Context, installationID int64) (*config.App, error) {
	if len(a.cfg.Apps) == 1 {
		return a.cfg.Apps[0], nil
	}
	if name, ok := a.knownApp(installationID); ok {
		return a.app(name)
	}
	for _, app := range a.cfg.Apps {
		if _, err := a.ListInstallations(ctx, &workflows.ListInstallationsInput{App: app.Name}); err != nil {
			return nil, err
		}
	}
	if name, ok := a.knownApp(installationID); ok {
		return a.app(name)
	}
	return nil, fmt.Errorf("installation %d is in no configured app's listing", installationID)
}

func (a *Activities) knownApp(installationID int64) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	name, ok := a.appOf[installationID]
	return name, ok
}

// installation returns the installation's client.
func (a *Activities) installation(ctx context.Context, installationID int64) (InstallationAPI, error) {
	app, err := a.appFor(ctx, installationID)
	if err != nil {
		return nil, err
	}
	return a.gh.Installation(app, installationID, a.cfg.Renovate.ConfigPath)
}

func (a *Activities) remember(app string, installationID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.appOf[installationID] = app
}

// ListInstallations lists the app's installations, filtered by the
// config allowlist, with each one's suspended flag; the workflow signals
// suspend to each installation's budget accordingly.
func (a *Activities) ListInstallations(ctx context.Context, in *workflows.ListInstallationsInput) ([]workflows.InstallationInfo, error) {
	app, err := a.app(in.App)
	if err != nil {
		return nil, err
	}
	api, err := a.gh.App(app)
	if err != nil {
		return nil, err
	}
	installs, err := api.ListInstallations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list installations of %s: %w", app.Name, err)
	}
	out := make([]workflows.InstallationInfo, 0, len(installs))
	for i := range installs {
		in := &installs[i]
		if len(app.Installations) > 0 && !slices.Contains(app.Installations, in.ID) {
			continue
		}
		a.remember(app.Name, in.ID)
		out = append(out, workflows.InstallationInfo{ID: in.ID, Account: in.Account, Suspended: in.Suspended()})
	}
	return out, nil
}

// Register registers every activity on r under its workflows package
// name. The budget activity registers separately (Budget.Register).
func (a *Activities) Register(r Registry) {
	for name, fn := range map[string]any{
		workflows.ListInstallationsActivity: a.ListInstallations,
		workflows.CheckRepoActivity:         a.CheckRepo,
		workflows.ReadRateLimitActivity:     a.ReadRateLimit,
	} {
		r.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
}
