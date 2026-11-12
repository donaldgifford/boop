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

package worker

import (
	"context"
	"log/slog"
	"time"
)

// SandboxCheckInterval is how often the worker refreshes
// boopd_sandbox_ready (IMPL-0006 task 9.2).
const SandboxCheckInterval = time.Minute

// RuntimeClassChecker is what WatchSandbox asks; kube.Runner implements
// it.
type RuntimeClassChecker interface {
	RuntimeClassExists(ctx context.Context, name string) (bool, error)
}

// WatchSandbox records whether the RuntimeClass exists now and every
// interval until ctx ends. A failed check records not ready and logs.
// It never touches /readyz: a missing sandbox fails sandboxed runs
// closed but must not stop ordinary ones.
func WatchSandbox(ctx context.Context, rc RuntimeClassChecker, class string, interval time.Duration,
	set func(class string, ready bool), log *slog.Logger,
) {
	check := func() {
		ok, err := rc.RuntimeClassExists(ctx, class)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn("sandbox check failed", "runtime_class", class, "error", err)
		}
		set(class, ok && err == nil)
	}
	check()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}
