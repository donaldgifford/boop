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
	"testing"

	"github.com/donaldgifford/boop/internal/renovate"
	"github.com/donaldgifford/boop/internal/workflows"
)

func TestCapDependencies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n             int
		wantLen       int
		wantTruncated bool
	}{
		{0, 0, false},
		{3, 3, false},
		{workflows.MaxDependencies, workflows.MaxDependencies, false},
		{workflows.MaxDependencies + 1, workflows.MaxDependencies, true},
	}
	for _, tt := range tests {
		deps := make([]renovate.Dependency, tt.n)
		for i := range deps {
			deps[i] = renovate.Dependency{Manager: "gomod", PackageName: "example.com/x"}
		}
		got, truncated := capDependencies(deps)
		if len(got) != tt.wantLen || truncated != tt.wantTruncated {
			t.Errorf("capDependencies(%d) = %d, %v; want %d, %v", tt.n, len(got), truncated, tt.wantLen, tt.wantTruncated)
		}
		if tt.n > 0 && got[0].PackageName != "example.com/x" {
			t.Errorf("capDependencies(%d)[0] = %+v", tt.n, got[0])
		}
	}
}
