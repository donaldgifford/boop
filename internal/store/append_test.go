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

package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/store"
)

func TestAppend_RejectsBeforeTouchingTheDatabase(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		e    store.Event
		want string
	}{
		{"no key", store.Event{OccurredAt: at, Kind: store.KindRepoOnboarded, Data: &store.Repository{V: 1}}, "no key"},
		{"no time", store.Event{Key: "k", Kind: store.KindRepoOnboarded, Data: &store.Repository{V: 1}}, "no occurred_at"},
		{"unknown kind", store.Event{Key: "k", OccurredAt: at, Kind: "nope", Data: &store.Repository{V: 1}}, "unknown event kind"},
		{"no data", store.Event{Key: "k", OccurredAt: at, Kind: store.KindRepoOnboarded}, "no data"},
		{"wrong data", store.Event{Key: "k", OccurredAt: at, Kind: store.KindRepoOnboarded, Data: &store.RunStarted{V: 1}}, "want *store.Repository"},
		{"no version", store.Event{Key: "k", OccurredAt: at, Kind: store.KindRepoOnboarded, Data: &store.Repository{}}, "version 0"},
	}
	s := store.New(nil, "test", nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := s.Append(t.Context(), tt.e)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Append(%s) = %v, want an error containing %q", tt.name, err, tt.want)
			}
		})
	}
	if err := s.Append(t.Context()); err != nil {
		t.Errorf("Append() = %v, want nil", err)
	}
	if err := s.Append(
		t.Context(),
		store.Event{Key: "k", OccurredAt: at, Kind: "x.y", Data: &store.Recheck{V: 1}},
	); !errors.Is(
		err,
		store.ErrUnknownKind,
	) {
		t.Errorf("Append(x.y) = %v, want ErrUnknownKind", err)
	}
}
