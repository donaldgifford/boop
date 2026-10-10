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

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/profiles"
	"github.com/donaldgifford/boop/internal/workflows"
)

// PlanRun resolves the next run's profile from the repository's extends
// and managers, and returns its app's cadence. The config file never
// travels in a payload, so RepoWorkflow asks here before each run.
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
	return &workflows.RunPlan{Profile: res.Resolve(in.Extends, in.Managers), Cadence: cadence}, nil
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
