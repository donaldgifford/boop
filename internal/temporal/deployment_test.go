package temporal

import (
	"testing"

	"go.temporal.io/sdk/worker"
)

func TestDecide(t *testing.T) {
	t.Parallel()

	v := func(id string) *worker.WorkerDeploymentVersion {
		return &worker.WorkerDeploymentVersion{DeploymentName: DefaultDeployment, BuildID: id}
	}

	tests := []struct {
		name    string
		current *worker.WorkerDeploymentVersion
		build   string
		want    promotion
	}{
		{"no current version", nil, "0.2.0-rc.2", promoteNow},
		{"empty current build", v(""), "0.2.0-rc.2", promoteNow},
		{"already current", v("0.2.0-rc.2"), "0.2.0-rc.2", alreadyCurrent},
		{"newer rc", v("0.2.0-rc.1"), "0.2.0-rc.2", promoteNow},
		{"release over rc", v("0.2.0-rc.9"), "0.2.0", promoteNow},
		{"older rc stands down", v("0.2.0-rc.2"), "0.2.0-rc.1", superseded},
		{"rc under release stands down", v("0.2.0"), "0.2.0-rc.9", superseded},
		{"v prefix compares", v("v0.3.0"), "0.2.0", superseded},
		{"dev never displaces a version", v("0.2.0"), "dev", superseded},
		{"revision never displaces a version", v("0.2.0"), "0123456789ab", superseded},
		{"version displaces dev", v("dev"), "0.2.0", promoteNow},
		{"dev over dev build", v("0123456789ab"), "dev", promoteNow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := decide(tt.current, tt.build); got != tt.want {
				t.Errorf("decide(%v, %q) = %d, want %d", tt.current, tt.build, got, tt.want)
			}
		})
	}
}
