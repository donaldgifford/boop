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
	"slices"
	"testing"
)

// TestFloor_IsConsistentWithTheImage holds the hand-kept floor to the
// generated facts: every floor manager exists, can run a command, and
// has a static default pattern list for the forced disable (task 5.3).
func TestFloor_IsConsistentWithTheImage(t *testing.T) {
	t.Parallel()
	if !slices.IsSorted(floor) {
		t.Errorf("floor is not sorted: %v", floor)
	}
	for _, m := range Floor() {
		if !IsManager(m) {
			t.Errorf("floor manager %q is not a Renovate manager", m)
		}
		if !slices.Contains(ExecManagers(), m) {
			t.Errorf("floor manager %q does not reach util/exec in Renovate %s", m, RenovateVersion)
		}
		if _, ok := DefaultFilePatterns(m); !ok {
			t.Errorf("floor manager %q has no static default managerFilePatterns", m)
		}
	}
}

func TestGenerated(t *testing.T) {
	t.Parallel()
	if want := []string{"bazelModDeps", "goGenerate", "gradleWrapper", "mise", "pixi"}; !slices.Equal(Gated(), want) {
		t.Errorf("Gated() = %v, want %v", Gated(), want)
	}
	if !IsGated("gradleWrapper") || IsGated("pipCompile") {
		t.Error("IsGated disagrees with Gated()")
	}
	p, ok := DefaultFilePatterns("pip-compile")
	if !ok || len(p) != 0 {
		t.Errorf("DefaultFilePatterns(pip-compile) = %v, %v; want an empty list that exists", p, ok)
	}
	if _, ok := DefaultFilePatterns("flux"); ok {
		t.Error("flux builds its patterns at run time; want no static entry")
	}
	if IsManager("not-a-manager") {
		t.Error("IsManager(not-a-manager) = true")
	}
}

func TestDefaultFilePatterns_ReturnsACopy(t *testing.T) {
	t.Parallel()
	p, _ := DefaultFilePatterns("bundler")
	p[0] = "changed"
	if again, _ := DefaultFilePatterns("bundler"); again[0] == "changed" {
		t.Error("DefaultFilePatterns returned the package's own slice")
	}
}
