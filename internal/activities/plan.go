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
	"slices"

	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/profiles"
	"github.com/donaldgifford/boop/internal/renovate/unsafe"
	"github.com/donaldgifford/boop/internal/workflows"
)

// PlanRun resolves the next run's profile from the repository's extends
// and managers, and returns its app's cadence and the ingest settle and
// min_interval. The config file never
// travels in a payload, so RepoWorkflow asks here before each run.
//
// With a floor, PlanRun first lists the default branch's tree (one REST
// call) and adds the floor managers whose default patterns match to the
// known ones; a truncated listing counts as a match. A failed listing is
// logged and counted, and the plan resolves from the known managers
// (IMPL-0006 Phase 7). In report mode a floor-raised plan keeps the
// ordinary profile and says FloorRaised.
func (a *Activities) PlanRun(ctx context.Context, in *workflows.PlanRunInput) (*workflows.RunPlan, error) {
	app, err := a.appFor(ctx, in.InstallationID)
	if err != nil {
		return nil, err
	}
	res, err := a.resolver()
	if err != nil {
		return nil, err
	}
	cadence := app.Cadence
	if cadence <= 0 {
		cadence = config.DefaultCadence
	}
	plan := &workflows.RunPlan{
		Cadence:     cadence,
		Settle:      a.cfg.Ingest.Settle,
		MinInterval: a.cfg.Ingest.MinInterval,
	}
	managers, truncated := in.Managers, false
	if res.HasFloor() && in.RepoID > 0 && in.DefaultBranch != "" {
		found, cut, err := a.detectFloor(ctx, in, res.FloorManagers())
		if err != nil {
			a.metrics.FloorDetectionError()
			activity.GetLogger(ctx).Warn("floor detection failed; resolving from known managers",
				"repo_id", in.RepoID, "error", err)
		}
		plan.FloorManagers, truncated = found, cut
		managers = union(managers, found)
	}
	profile, raised := res.Resolve(in.Extends, managers)
	if truncated && !raised {
		profile, raised = res.Raise(profile)
	}
	plan.FloorRaised = raised
	if raised && a.cfg.FloorMode() == config.FloorReport {
		// OQ7: report mode runs it as today and says what enforce would do.
		ordinary := res.ResolveRules(in.Extends, managers)
		activity.GetLogger(ctx).Info("floor would sandbox this repository; report mode runs it ordinary",
			"would_sandbox", true, "repo_id", in.RepoID, "profile", ordinary, "sandbox_profile", profile,
			"floor_managers", plan.FloorManagers, "truncated", truncated)
		profile = ordinary
	}
	plan.Profile, plan.Sandboxed = profile, res.Sandboxed(profile)
	return plan, nil
}

// detectFloor lists the repository's tree and returns the floor managers
// whose default patterns match, and whether the listing was truncated.
func (a *Activities) detectFloor(ctx context.Context, in *workflows.PlanRunInput, floor []string) ([]string, bool, error) {
	api, err := a.installation(ctx, in.InstallationID)
	if err != nil {
		return nil, false, err
	}
	tree, err := api.ListTree(ctx, in.RepoID, in.DefaultBranch)
	if err != nil {
		return nil, false, err
	}
	return unsafe.MatchFloor(floor, tree.Paths), tree.Truncated, nil
}

// union returns a with b's entries it lacks appended.
func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, v := range b {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// resolver builds the profile resolver once.
func (a *Activities) resolver() (*profiles.Resolver, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.profiles == nil {
		r, err := a.cfg.Resolver()
		if err != nil {
			return nil, fmt.Errorf("profile resolver: %w", err)
		}
		a.profiles = r
	}
	return a.profiles, nil
}
