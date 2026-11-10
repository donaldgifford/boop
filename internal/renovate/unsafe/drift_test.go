//go:build renovate

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

package unsafe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/donaldgifford/boop/internal/renovate/unsafe/distscan"
)

var pinnedImage = regexp.MustCompile(`(?m)^ {4}image: (ghcr\.io/renovatebot/renovate:\S+)$`)

// TestDrift reads the chart's pinned Renovate image and fails when it
// disagrees with zz_generated.go (IMPL-0006 task 2.3, OQ3). Run it with
// `just test-renovate`; it needs Docker.
func TestDrift(t *testing.T) {
	values, err := os.ReadFile("../../../charts/boopd/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := pinnedImage.FindSubmatch(values)
	if m == nil {
		t.Fatal("no pinned Renovate image in charts/boopd/values.yaml")
	}
	image := string(m[1])

	got, err := distscan.Scan(extractDist(t, image), image)
	if err != nil {
		t.Fatalf("scanning %s: %v", image, err)
	}

	var problems []string
	if image != SourceImage {
		problems = append(problems, fmt.Sprintf(
			"the chart pins %s but zz_generated.go came from %s", image, SourceImage))
	}
	problems = append(problems, listDrift("allowedUnsafeExecutions value", gated, got.Gated,
		"decide whether a sandboxed profile may grant it (unsafe_executions) and update DESIGN-0005's exec table")...)
	problems = append(problems, listDrift("manager whose artifacts reach util/exec", execManagers, got.ExecManagers,
		"decide whether it executes repository code ungated; if so add it to the floor in unsafe.go")...)
	problems = append(problems, listDrift("manager", managers, got.Managers,
		"check it against the floor")...)
	problems = append(problems, listDrift("manager with run-time default patterns", dynamicPatterns, got.DynamicPatterns,
		"a floor manager here cannot be force-disabled with its defaults")...)
	for _, name := range got.Managers {
		want, had := defaultFilePatterns[name]
		have, has := got.FilePatterns[name]
		if had != has || !slices.Equal(want, have) {
			problems = append(problems, fmt.Sprintf(
				"default managerFilePatterns of %s changed: %q -> %q; the forced disable and floor detection use them", name, want, have))
		}
	}

	if len(problems) > 0 {
		t.Errorf("Renovate %s in %s differs from zz_generated.go (Renovate %s); run `just generate-unsafe` after deciding:\n- %s",
			got.Version, image, RenovateVersion, strings.Join(problems, "\n- "))
	}
}

// extractDist copies the image's /usr/local/renovate/{dist,package.json}
// out of a created, never started, container.
func extractDist(t *testing.T, image string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "docker", "create", image).Output()
	if err != nil {
		t.Fatalf("docker create %s: %v", image, err)
	}
	cid := strings.TrimSpace(string(out))
	// t.Context is canceled before cleanups run, so removal gets its own.
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "docker", "rm", "-f", cid).Run() })

	work := t.TempDir()
	for _, p := range []string{"dist", "package.json"} {
		cp := exec.CommandContext(t.Context(), "docker", "cp", "-q", cid+":/usr/local/renovate/"+p, filepath.Join(work, p))
		if b, err := cp.CombinedOutput(); err != nil {
			t.Fatalf("docker cp %s: %v: %s", p, err, b)
		}
	}
	return filepath.Join(work, "dist")
}
