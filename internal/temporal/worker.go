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

package temporal

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/sysinfo"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// DefaultDeployment is boopd's one worker deployment: one role on one
// task queue (DESIGN-0001 § Topology and packages), so the fleet
// promotes and rolls back as a unit.
const DefaultDeployment = "boopd"

// DefaultActivityConcurrency is WORKER_ACTIVITY_CONCURRENCY's default:
// the activities one worker runs at once. A RunRenovate activity holds
// its slot for the whole run, so with two replicas this matches the
// cluster-wide runs.maxConcurrent default of 20 (DESIGN-0001 OQ12).
const DefaultActivityConcurrency = 10

// buildIDRevisionLen shortens a VCS revision used as a build ID.
const buildIDRevisionLen = 12

// devBuildID is the build ID of a binary with no version or revision.
const devBuildID = "dev"

// develVersion is the module version of an untagged build.
const develVersion = "(devel)"

// WorkerConfig configures a Temporal worker.
type WorkerConfig struct {
	TaskQueue string

	// Deployment is the worker deployment the worker joins; empty means
	// DefaultDeployment.
	Deployment string

	// ActivityConcurrency caps concurrent activity executions.
	ActivityConcurrency int

	// BuildID is this binary's deployment version. Workflows auto-upgrade
	// to the current version at their next workflow task, so a deploy
	// never strands running executions on old workers.
	BuildID string

	// StopTimeout is how long Stop waits for running activities. The SDK
	// default of zero cancels every run in flight at SIGTERM; boopd sets
	// it to RunRenovate's StartToClose so runs reach their soft
	// deadlines (DESIGN-0001 § RunRenovate activity).
	StopTimeout time.Duration
}

// WorkerConfigFromEnv reads WORKER_ACTIVITY_CONCURRENCY and
// TEMPORAL_BUILD_ID. The build ID defaults to BuildID().
func WorkerConfigFromEnv(cfg *Config) (WorkerConfig, error) {
	wc := WorkerConfig{
		TaskQueue:           cfg.TaskQueue,
		ActivityConcurrency: DefaultActivityConcurrency,
		BuildID:             envOr("TEMPORAL_BUILD_ID", BuildID()),
	}

	if v := os.Getenv("WORKER_ACTIVITY_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return WorkerConfig{}, fmt.Errorf("temporal: WORKER_ACTIVITY_CONCURRENCY=%q: want a positive integer", v)
		}

		wc.ActivityConcurrency = n
	}

	return wc, nil
}

// DeploymentName is the worker deployment the worker joins.
func (wc *WorkerConfig) DeploymentName() string { return wc.deployment() }

func (wc *WorkerConfig) deployment() string {
	if wc.Deployment == "" {
		return DefaultDeployment
	}

	return wc.Deployment
}

// DevBuild reports whether the build ID fell through to "dev": no
// TEMPORAL_BUILD_ID, no module version, no VCS revision. Two different
// dev images then look like one version to Temporal.
func (wc *WorkerConfig) DevBuild() bool { return wc.BuildID == devBuildID }

// NewWorker returns a worker on wc's task queue with deployment
// versioning on: the build ID is the binary version and workflows
// default to AutoUpgrade. Callers register workflows and activities,
// then Run or Start it. A versioned worker is dispatched no tasks until
// PromoteBuild makes its build the deployment's current version.
func NewWorker(c client.Client, wc *WorkerConfig) worker.Worker {
	return worker.New(c, wc.TaskQueue, workerOptions(wc))
}

func workerOptions(wc *WorkerConfig) worker.Options {
	return worker.Options{
		MaxConcurrentActivityExecutionSize: wc.ActivityConcurrency,
		WorkerStopTimeout:                  wc.StopTimeout,
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version: worker.WorkerDeploymentVersion{
				DeploymentName: wc.deployment(),
				BuildID:        wc.BuildID,
			},
			DefaultVersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
		},
		// Worker heartbeats (on by default since SDK 1.41) report 0 for
		// CPU and memory without a provider. This one reads the pod's
		// cgroup limits, so the numbers are the container's, not the
		// node's.
		SysInfoProvider: sysinfo.SysInfoProvider(),
	}
}

// BuildID returns the binary's version: the module version for a tagged
// build, otherwise the VCS revision, otherwise "dev".
func BuildID() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return devBuildID
	}

	return buildIDFrom(info)
}

func buildIDFrom(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != develVersion {
		return v
	}

	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			return s.Value[:min(len(s.Value), buildIDRevisionLen)]
		}
	}

	return devBuildID
}
