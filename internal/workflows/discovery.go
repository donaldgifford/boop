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

package workflows

import (
	"slices"

	"go.temporal.io/sdk/workflow"
)

// DiscoveryWorkflow is one discovery pass for one App, started by the
// discovery/<app> Schedule (DESIGN-0001 § DiscoveryWorkflow) or, for
// named installations only, by ingest (DESIGN-0003 § Targeted
// discovery): list the
// installations, tell each budget whether it is suspended, then run one
// DiscoverInstallation per active installation in parallel. Repository
// lists never enter the workflow; each activity signals the
// RepoWorkflows itself. A failed installation is counted, not fatal, so
// one bad installation never blocks the others.
func DiscoveryWorkflow(ctx workflow.Context, in *DiscoveryInput) (*DiscoveryResult, error) {
	log := workflow.GetLogger(ctx)
	var installs []InstallationInfo
	err := workflow.ExecuteActivity(shortOptions(ctx, PriorityNormal, 0), ListInstallationsActivity,
		&ListInstallationsInput{App: in.App}).Get(ctx, &installs)
	if err != nil {
		return nil, err
	}
	if len(in.Installations) > 0 {
		installs = targeted(ctx, installs, in.Installations)
	}

	out := &DiscoveryResult{App: in.App, Installations: len(installs)}
	signals := make([]workflow.Future, 0, len(installs))
	for i := range installs {
		signals = append(signals, workflow.SignalExternalWorkflow(ctx, InstallationWorkflowID(installs[i].ID), "",
			SuspendSignal, Suspend{Suspended: installs[i].Suspended}))
	}

	type pass struct {
		id     int64
		future workflow.Future
	}
	passes := make([]pass, 0, len(installs))
	for i := range installs {
		if installs[i].Suspended {
			out.Suspended++
			continue
		}
		id := installs[i].ID
		passes = append(passes, pass{id, workflow.ExecuteActivity(discoverOptions(ctx, id), DiscoverInstallationActivity,
			&DiscoverInput{App: in.App, InstallationID: id})})
	}

	// A budget that does not exist yet has nothing to suspend.
	for i, f := range signals {
		if err := f.Get(ctx, nil); err != nil {
			log.Debug("suspend signal not delivered", "installation", installs[i].ID, "error", err)
		}
	}
	for _, p := range passes {
		var sum DiscoverSummary
		if err := p.future.Get(ctx, &sum); err != nil {
			out.Failed++
			log.Warn("installation discovery failed", "installation", p.id, "error", err)
			continue
		}
		out.Seen += sum.Seen
		out.Onboarded += sum.Onboarded
		out.ProbeCost += sum.ProbeCost
	}
	return out, nil
}

// targeted keeps the listed installations whose IDs were asked for. The
// listing is already filtered by the App's allowlist, so an asked-for ID
// missing from it is outside the allowlist or not the App's: skipped
// with a warn.
func targeted(ctx workflow.Context, installs []InstallationInfo, ids []int64) []InstallationInfo {
	out := make([]InstallationInfo, 0, len(ids))
	for _, id := range ids {
		i := slices.IndexFunc(installs, func(in InstallationInfo) bool { return in.ID == id })
		if i < 0 {
			workflow.GetLogger(ctx).Warn("targeted installation skipped: not in the app's allowlisted listing", "installation", id)
			continue
		}
		out = append(out, installs[i])
	}
	return out
}
