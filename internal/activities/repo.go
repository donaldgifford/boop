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
	"errors"
	"fmt"

	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/workflows"
)

// CheckRepo answers RepoWorkflow's absence timer and its changed signal:
// gone, no-config or present, with the current slug, default branch and
// extends. A deleted installation (its mint answers 404, or it is in no
// App's fresh listing) is gone. Could-not-tell is an error, so the
// workflow retries rather than ending on an outage (DESIGN-0001 §
// RepoWorkflow, DESIGN-0003 § CheckRepo returns extends).
func (a *Activities) CheckRepo(ctx context.Context, in *workflows.CheckRepoInput) (*workflows.CheckRepoResult, error) {
	gone := &workflows.CheckRepoResult{State: workflows.RepoGone}
	api, err := a.installation(ctx, in.InstallationID)
	if errors.Is(err, platform.ErrInstallationGone) {
		a.log.Info("installation gone", "installation", in.InstallationID, "repo_id", in.RepoID, "error", err)
		return gone, nil
	}
	if err != nil {
		return nil, err
	}
	check, err := api.CheckRepo(ctx, in.RepoID)
	if errors.Is(err, platform.ErrInstallationGone) {
		a.log.Info("installation gone", "installation", in.InstallationID, "repo_id", in.RepoID, "error", err)
		return gone, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check repository %d: %w", in.RepoID, err)
	}
	out := &workflows.CheckRepoResult{}
	switch check.State {
	case platform.RepoGone:
		out.State = workflows.RepoGone
	case platform.RepoNoConfig:
		out.State = workflows.RepoNoConfig
	case platform.RepoPresent:
		out.State = workflows.RepoPresent
		out.Extends = check.Config.Extends
		if check.Config.ParseErr != nil {
			a.log.Warn("config file does not parse; onboarded without its extends",
				"repo_id", in.RepoID, "path", check.Config.Path, "error", check.Config.ParseErr)
		}
	default:
		return nil, fmt.Errorf("check repository %d: no answer", in.RepoID)
	}
	if check.Repo != nil {
		out.Slug, out.DefaultBranch = check.Repo.Slug, check.Repo.DefaultBranch
	}
	return out, nil
}

// ReadRateLimit reads the installation's /rate_limit for its budget
// entity, and re-tunes the installation client's limiter from the
// discovered core limit.
func (a *Activities) ReadRateLimit(ctx context.Context, in *workflows.ReadRateLimitInput) (*workflows.Readings, error) {
	api, err := a.installation(ctx, in.InstallationID)
	if err != nil {
		return nil, err
	}
	r, err := api.ReadRateLimit(ctx)
	if err != nil {
		return nil, fmt.Errorf("read rate limit of installation %d: %w", in.InstallationID, err)
	}
	if core, ok := r.Resources["core"]; ok {
		api.SetDiscoveredLimit(core.Limit)
	}
	return r, nil
}
