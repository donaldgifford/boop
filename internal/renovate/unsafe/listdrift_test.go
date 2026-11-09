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
	"fmt"
	"slices"
	"strings"
	"testing"
)

// listDrift names entries added to or removed from a generated list,
// with the decision each one needs. The drift test (build tag renovate)
// uses it.
func listDrift(what string, generated, image []string, decision string) []string {
	var out []string
	for _, v := range image {
		if !slices.Contains(generated, v) {
			out = append(out, fmt.Sprintf("new %s %q: %s", what, v, decision))
		}
	}
	for _, v := range generated {
		if !slices.Contains(image, v) {
			out = append(out, fmt.Sprintf("%s %q is gone: %s", what, v, decision))
		}
	}
	return out
}

// The drift test fails when the generated file lacks an entry the image
// has, and passes when they agree (IMPL-0006 Phase 2 success criteria).
func TestListDrift(t *testing.T) {
	t.Parallel()
	if got := listDrift("value", gated, Gated(), "decide"); len(got) != 0 {
		t.Errorf("listDrift(committed, committed) = %v, want none", got)
	}
	removed := slices.DeleteFunc(Gated(), func(v string) bool { return v == "gradleWrapper" })
	got := listDrift("allowedUnsafeExecutions value", removed, gated, "decide")
	if len(got) != 1 || !strings.Contains(got[0], `new allowedUnsafeExecutions value "gradleWrapper"`) {
		t.Errorf("listDrift(entry removed from the generated list) = %v, want it named", got)
	}
}
