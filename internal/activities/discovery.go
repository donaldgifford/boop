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
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/platform/github"
	"github.com/donaldgifford/boop/internal/workflows"
)

// Starter is the part of client.Client DiscoverInstallation uses.
type Starter interface {
	SignalWithStartWorkflow(ctx context.Context, workflowID, signalName string, signalArg any,
		options client.StartWorkflowOptions, workflow any, workflowArgs ...any) (client.WorkflowRun, error)
}

// reserveTick bounds a reserve sleep between heartbeats.
const reserveTick = 10 * time.Second

// DiscoverInstallation is one installation's whole discovery pass
// (DESIGN-0001 § DiscoveryWorkflow): page the repositories, probe each
// page for the config file, and SignalWithStart repo/github/<id> with
// discovered for every repository that has it. No repository list
// leaves the activity. It heartbeats {page, seen, onboarded} after each
// page, and a retry skips the pages its last heartbeat covered.
func (a *Activities) DiscoverInstallation(ctx context.Context, in *workflows.DiscoverInput) (*workflows.DiscoverSummary, error) {
	app, err := a.app(in.App)
	if err != nil {
		return nil, err
	}
	api, err := a.gh.Installation(app, in.InstallationID, a.cfg.Renovate.ConfigPath)
	if err != nil {
		return nil, err
	}
	a.remember(app.Name, in.InstallationID)

	var resume workflows.DiscoverProgress
	if activity.HasHeartbeatDetails(ctx) {
		if err := activity.GetHeartbeatDetails(ctx, &resume); err != nil {
			resume = workflows.DiscoverProgress{}
		}
	}
	prog := resume
	sum := &workflows.DiscoverSummary{InstallationID: in.InstallationID}
	filter := platform.DiscoveryFilter{SkipForks: app.Discovery.SkipForks, SkipArchived: app.Discovery.SkipArchived}
	err = api.DiscoverPages(ctx, filter, func(p github.Page) error {
		sum.Pages = p.Number
		if p.Number <= resume.Page {
			return nil
		}
		if err := a.awaitReserve(ctx, api, app, prog); err != nil {
			return err
		}
		onboarded, cost, err := a.discoverPage(ctx, api, app, in.InstallationID, p.Repos)
		if err != nil {
			return err
		}
		sum.ProbeCost += cost
		prog = workflows.DiscoverProgress{Page: p.Number, Seen: prog.Seen + p.Seen, Onboarded: prog.Onboarded + onboarded}
		activity.RecordHeartbeat(ctx, prog)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover installation %d: %w", in.InstallationID, err)
	}
	sum.Seen, sum.Onboarded = prog.Seen, prog.Onboarded
	return sum, nil
}

// awaitReserve reads /rate_limit and, while a tracked resource is under
// the app's reserve, sleeps to its reset, heartbeating. Discovery takes
// no lease; its spend shows in the budget's next readings.
func (a *Activities) awaitReserve(ctx context.Context, api InstallationAPI, app *config.App, prog workflows.DiscoverProgress) error {
	for {
		r, err := api.ReadRateLimit(ctx)
		if err != nil {
			return fmt.Errorf("read rate limit: %w", err)
		}
		if core, ok := r.Resources[workflows.ResourceCore]; ok {
			api.SetDiscoveredLimit(core.Limit)
		}
		until, low := underReserve(r, app.Budget.ReserveFraction, a.now())
		if !low {
			return nil
		}
		a.log.InfoContext(ctx, "discovery under reserve, sleeping to reset", "app", app.Name, "until", until)
		for wait := until.Sub(a.now()); wait > 0; wait = until.Sub(a.now()) {
			t := time.NewTimer(min(wait, reserveTick))
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
			activity.RecordHeartbeat(ctx, prog)
		}
	}
}

// underReserve reports whether a tracked resource is under the reserve
// and the latest reset among those that are. A resource whose reset has
// passed has refilled, so it does not count.
func underReserve(r *workflows.Readings, fraction float64, now time.Time) (time.Time, bool) {
	var until time.Time
	for _, name := range workflows.TrackedResources {
		res, ok := r.Resources[name]
		if !ok || res.Limit <= 0 || !res.Reset.After(now) {
			continue
		}
		if float64(res.Remaining) < fraction*float64(res.Limit) && res.Reset.After(until) {
			until = res.Reset
		}
	}
	return until, !until.IsZero()
}

// discoverPage probes one page and signals every repository with the
// file. It returns how many it signalled and the probe's cost.
func (a *Activities) discoverPage(ctx context.Context, api InstallationAPI, app *config.App,
	installationID int64, repos []platform.Repository,
) (onboarded, cost int, err error) {
	if len(repos) == 0 {
		return 0, 0, nil
	}
	path := a.cfg.Renovate.ConfigPath
	var res *github.ProbeResult
	if app.Discovery.Probe == config.ProbeREST {
		res, err = api.ProbeConfigREST(ctx, repos, path)
	} else {
		ids := make([]string, len(repos))
		for i := range repos {
			ids[i] = repos[i].NodeID
		}
		res, err = api.ProbeConfig(ctx, ids, path)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("probe: %w", err)
	}
	a.metrics.ProbeCost(app.Name, res.Cost)

	now := a.now()
	for i := range repos {
		repo := &repos[i]
		probe, ok := res.Configs[repo.NodeID]
		if !ok || !probe.Exists {
			continue
		}
		if err := a.signalDiscovered(ctx, app, installationID, repo, probe.Extends, now); err != nil {
			return onboarded, res.Cost, err
		}
		onboarded++
	}
	return onboarded, res.Cost, nil
}

// signalDiscovered starts repo/github/<id> if it is not running and
// sends it discovered.
func (a *Activities) signalDiscovered(ctx context.Context, app *config.App, installationID int64,
	repo *platform.Repository, extends []string, now time.Time,
) error {
	sig := workflows.Discovered{
		Slug:              repo.Slug,
		DefaultBranch:     repo.DefaultBranch,
		InstallationID:    installationID,
		DiscoveryInterval: app.Discovery.Every,
		Extends:           extends,
	}
	state := &workflows.RepoState{
		Platform:          workflows.Platform,
		RepoID:            repo.ID,
		Slug:              repo.Slug,
		DefaultBranch:     repo.DefaultBranch,
		InstallationID:    installationID,
		DiscoveryInterval: app.Discovery.Every,
		Extends:           extends,
		LastSeen:          now,
	}
	id := workflows.RepoWorkflowID(repo.ID)
	opts := client.StartWorkflowOptions{ID: id, TaskQueue: a.taskQueue}
	if _, err := a.temporal.SignalWithStartWorkflow(ctx, id, workflows.DiscoveredSignal, sig, opts, workflows.RepoWorkflowName, state); err != nil {
		return fmt.Errorf("signal %s: %w", id, err)
	}
	return nil
}
