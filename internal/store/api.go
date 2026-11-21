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
	"errors"
	"fmt"

	"github.com/donaldgifford/boop/internal/store/db"
)

// ErrNotFound is a read of one resource that is unknown or outside the
// caller's scope; the API answers both 404.
var ErrNotFound = errors.New("not found")

// Ping checks the store answers, for the API role's readiness.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping store: %w", err)
	}
	return nil
}

// Owners is the installation accounts in scope the store knows,
// lowercased and sorted: /me's owner list.
func (s *Store) Owners(ctx context.Context, scope Scope) ([]string, error) {
	owners, err := db.New(s.pool).VisibleOwners(ctx, db.VisibleOwnersParams{AllOwners: scope.All, Owners: scope.Owners})
	if err != nil {
		return nil, fmt.Errorf("read owners: %w", err)
	}
	return owners, nil
}
