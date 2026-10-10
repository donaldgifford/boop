package temporal

import (
	"log/slog"
	"runtime/debug"
	"testing"

	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

func TestWorkerConfigFromEnv(t *testing.T) {
	cfg := &Config{TaskQueue: DefaultTaskQueue}

	t.Run("defaults", func(t *testing.T) {
		wc, err := WorkerConfigFromEnv(cfg)
		if err != nil {
			t.Fatalf("WorkerConfigFromEnv: %v", err)
		}

		if wc.ActivityConcurrency != DefaultActivityConcurrency || wc.TaskQueue != DefaultTaskQueue || wc.BuildID == "" {
			t.Errorf("config = %+v", wc)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		t.Setenv("WORKER_ACTIVITY_CONCURRENCY", "25")
		t.Setenv("TEMPORAL_BUILD_ID", "v0.2.0-rc.1")

		wc, err := WorkerConfigFromEnv(cfg)
		if err != nil {
			t.Fatalf("WorkerConfigFromEnv: %v", err)
		}

		if wc.ActivityConcurrency != 25 || wc.BuildID != "v0.2.0-rc.1" {
			t.Errorf("config = %+v", wc)
		}
	})

	for _, bad := range []string{"0", "-1", "ten"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			t.Setenv("WORKER_ACTIVITY_CONCURRENCY", bad)

			if _, err := WorkerConfigFromEnv(cfg); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestBuildIDFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{"tagged", debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}}, "v0.2.0"},
		{
			"devel with revision",
			debug.BuildInfo{
				Main:     debug.Module{Version: develVersion},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}},
			},
			"0123456789ab",
		},
		{"nothing", debug.BuildInfo{Main: debug.Module{Version: develVersion}}, devBuildID},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := buildIDFrom(&tt.info); got != tt.want {
				t.Errorf("buildIDFrom = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWorkerOptions_ReportsHostResources(t *testing.T) {
	t.Parallel()

	opts := workerOptions(&WorkerConfig{TaskQueue: DefaultTaskQueue, ActivityConcurrency: 3, BuildID: "b"})

	if opts.MaxConcurrentActivityExecutionSize != 3 {
		t.Errorf("MaxConcurrentActivityExecutionSize = %d, want 3", opts.MaxConcurrentActivityExecutionSize)
	}

	// Without a provider the SDK's worker heartbeats report 0 for CPU and
	// memory, so a real reading is what proves the option is wired.
	if opts.SysInfoProvider == nil {
		t.Fatal("SysInfoProvider is nil; worker heartbeats would report 0 CPU and memory")
	}

	mem, err := opts.SysInfoProvider.MemoryUsage(&worker.SysInfoContext{Logger: tlog.NewStructuredLogger(slog.Default())})
	if err != nil {
		t.Fatalf("MemoryUsage: %v", err)
	}

	if mem <= 0 || mem > 1 {
		t.Errorf("MemoryUsage = %v, want a fraction in (0, 1]", mem)
	}
}

// TestWorkerOptions_Versioning: the worker joins boopd's one deployment
// under its build ID, with workflows auto-upgrading.
func TestWorkerOptions_Versioning(t *testing.T) {
	t.Parallel()

	got := workerOptions(&WorkerConfig{TaskQueue: DefaultTaskQueue, BuildID: "b"}).DeploymentOptions
	if !got.UseVersioning || got.Version.DeploymentName != DefaultDeployment || got.Version.BuildID != "b" {
		t.Errorf("DeploymentOptions = %+v, want versioning on %s/b", got, DefaultDeployment)
	}

	got = workerOptions(&WorkerConfig{TaskQueue: DefaultTaskQueue, Deployment: "canary", BuildID: "b"}).DeploymentOptions
	if got.Version.DeploymentName != "canary" {
		t.Errorf("deployment = %q, want the override", got.Version.DeploymentName)
	}
}
