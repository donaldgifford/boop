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

package platform_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/platform"
)

// TestDefaultConfigPaths pins Renovate's GitHub lookup order: the first
// present file is the one Renovate reads, so discovery must parse the
// same one. Includes every name repo-guardian accepts.
func TestDefaultConfigPaths(t *testing.T) {
	t.Parallel()
	want := []string{
		"renovate.json", "renovate.jsonc", "renovate.json5",
		".github/renovate.json", ".github/renovate.jsonc", ".github/renovate.json5",
		".renovaterc", ".renovaterc.json", ".renovaterc.jsonc", ".renovaterc.json5",
	}
	if !slices.Equal(platform.DefaultConfigPaths, want) {
		t.Errorf("DefaultConfigPaths = %q, want %q", platform.DefaultConfigPaths, want)
	}
}

func TestRateLimitedError_WrapsTransient(t *testing.T) {
	t.Parallel()

	err := &platform.RateLimitedError{RetryAfter: 30 * time.Second, Cause: errors.New("429")}
	if !errors.Is(err, platform.ErrTransient) {
		t.Error("RateLimitedError should be ErrTransient")
	}
	if !strings.Contains(err.Error(), "30s") {
		t.Errorf("Error() = %q, want retry-after duration", err.Error())
	}

	zero := &platform.RateLimitedError{}
	if zero.Error() == "" || strings.Contains(zero.Error(), "0s") {
		t.Errorf("Error() with zero RetryAfter = %q, should omit duration", zero.Error())
	}
}

func TestErrorSentinelsAreDistinct(t *testing.T) {
	t.Parallel()

	if errors.Is(platform.ErrTransient, platform.ErrPermanent) {
		t.Error("ErrTransient and ErrPermanent must be distinct")
	}
	if !errors.Is(platform.ErrUnauthorized, platform.ErrUnauthorized) {
		t.Error("errors.Is must match itself")
	}
}
