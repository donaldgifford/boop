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

package store

import (
	"context"
	"fmt"
	"regexp"

	"github.com/donaldgifford/boop/internal/store/db"
)

// ReceivedDeliveries reports which of the delivery GUIDs ingest recorded
// with delivery.received. The delivery audit calls a GUID GitHub
// delivered with a 2xx and ingest never recorded missed (DESIGN-0003 §
// Delivery audit). GUIDs that are not UUIDs are never recorded, so they
// are left out of the query and absent from the answer.
func (s *Store) ReceivedDeliveries(ctx context.Context, guids []string) (map[string]bool, error) {
	ids := make([]string, 0, len(guids))
	for _, g := range guids {
		if IsUUID(g) {
			ids = append(ids, g)
		}
	}
	got := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return got, nil
	}
	rows, err := db.New(s.pool).ReceivedDeliveries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("received deliveries: %w", err)
	}
	for _, id := range rows {
		got[id] = true
	}
	return got, nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s is a UUID, as GitHub's delivery GUIDs are and
// webhook_deliveries.delivery_id must be.
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }

// PRRef names one pull request.
type PRRef struct {
	RepoID int64
	Number int
}

// OpenPullRequests is the PRs the store holds open in the
// installation's repositories, ordered by repository and number. The PR
// reconcile reads them to find the PRs that left GitHub's open set.
func (s *Store) OpenPullRequests(ctx context.Context, installationID int64) ([]PRRef, error) {
	rows, err := db.New(s.pool).OpenPullRequests(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("open pull requests of %d: %w", installationID, err)
	}
	out := make([]PRRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, PRRef{RepoID: r.RepoID, Number: int(r.Number)})
	}
	return out, nil
}
