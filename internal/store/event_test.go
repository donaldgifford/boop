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
	"encoding/json"
	"errors"
	"testing"

	"github.com/donaldgifford/boop/internal/store"
)

func TestDecodeData_EveryKindRoundTrips(t *testing.T) {
	t.Parallel()
	for _, kind := range store.Kinds() {
		d, err := store.DecodeData(kind, []byte(`{"v":1}`))
		if err != nil {
			t.Errorf("DecodeData(%s) = %v", kind, err)
			continue
		}
		if d.Version() != 1 {
			t.Errorf("DecodeData(%s).Version() = %d, want 1", kind, d.Version())
		}
		if _, err := json.Marshal(d); err != nil {
			t.Errorf("marshal %s: %v", kind, err)
		}
	}
}

func TestDecodeData_UnknownKind(t *testing.T) {
	t.Parallel()
	if _, err := store.DecodeData("nope.kind", []byte(`{}`)); !errors.Is(err, store.ErrUnknownKind) {
		t.Errorf("DecodeData(nope.kind) = %v, want ErrUnknownKind", err)
	}
}
