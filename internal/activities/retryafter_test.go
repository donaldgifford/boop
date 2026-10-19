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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/temporal"

	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/workflows"
)

func TestHonourRetryAfter(t *testing.T) {
	t.Parallel()
	plain := errors.New("boom")
	noWait := fmt.Errorf("list: %w", &platform.RateLimitedError{})
	waited := fmt.Errorf("discover installation 1: %w", &platform.RateLimitedError{RetryAfter: 7 * time.Second})

	if got := honourRetryAfter(nil); got != nil {
		t.Errorf("honourRetryAfter(nil) = %v, want nil", got)
	}
	if got := honourRetryAfter(plain); !errors.Is(got, plain) || isAppErr(got) {
		t.Errorf("plain error = %v, want it unchanged", got)
	}
	if got := honourRetryAfter(noWait); !errors.Is(got, noWait) || isAppErr(got) {
		t.Errorf("rate limit without a wait = %v, want it unchanged (the policy's backoff applies)", got)
	}

	got := honourRetryAfter(waited)
	appErr, ok := errors.AsType[*temporal.ApplicationError](got)
	if !ok {
		t.Fatalf("rate limit with a wait = %T %v, want *temporal.ApplicationError", got, got)
	}
	if appErr.Type() != workflows.ErrTypeGitHubRateLimited {
		t.Errorf("Type() = %q, want %q", appErr.Type(), workflows.ErrTypeGitHubRateLimited)
	}
	if appErr.NextRetryDelay() != 7*time.Second {
		t.Errorf("NextRetryDelay() = %v, want 7s", appErr.NextRetryDelay())
	}
	if appErr.NonRetryable() {
		t.Error("NonRetryable() = true, want a retryable error")
	}
	if !strings.HasSuffix(appErr.Error(), waited.Error()) {
		t.Errorf("Error() = %q, want it to end with the original %q", appErr.Error(), waited.Error())
	}
	if !errors.Is(got, platform.ErrTransient) {
		t.Error("errors.Is(ErrTransient) = false, want the cause kept")
	}
}

func isAppErr(err error) bool {
	_, ok := errors.AsType[*temporal.ApplicationError](err)
	return ok
}
