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
	"context"
	"fmt"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/workflows"
)

// MaintainStore runs one of the store's retention rules as of in.Now
// with store.retention (DESIGN-0004 § Retention). With the store
// disabled it does nothing.
func (a *Activities) MaintainStore(ctx context.Context, in *workflows.MaintainStoreInput) (*workflows.MaintainStoreResult, error) {
	out := &workflows.MaintainStoreResult{Rule: in.Rule}
	if a.store == nil {
		return out, nil
	}
	r := a.cfg.Store.Retention
	n, err := a.store.Maintain(ctx, store.MaintenanceRule(in.Rule), in.Now, store.Retention{
		Events: r.Events, DeliveryPayloads: r.DeliveryPayloads, Offboarded: r.Offboarded,
	})
	if err != nil {
		return nil, fmt.Errorf("store maintenance %s: %w", in.Rule, err)
	}
	out.Removed = n
	a.log.Info("store maintenance rule", "rule", in.Rule, "removed", n)
	return out, nil
}
