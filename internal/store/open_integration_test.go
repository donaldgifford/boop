//go:build integration

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
	"testing"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
)

// TestOpenReadOnly: the API's pool starts every connection read-only
// with a 5 s statement timeout, so even a role granted more cannot
// write through it.
func TestOpenReadOnly(t *testing.T) {
	db := migrated(t)
	pool, err := store.OpenReadOnly(t.Context(), db.RoleDSN(storetest.RoleWorker))
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer pool.Close()
	var ro, timeout string
	if err := pool.QueryRow(t.Context(), "SHOW default_transaction_read_only").Scan(&ro); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SHOW statement_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if ro != "on" || timeout != "5s" {
		t.Errorf("read_only = %q, statement_timeout = %q, want on and 5s", ro, timeout)
	}
	_, err = pool.Exec(t.Context(), "DELETE FROM dashboards")
	if err == nil {
		t.Error("DELETE through the read-only pool succeeded")
	}
	st := store.New(pool, "boopd_ro", nil)
	if err := st.SchemaReady(t.Context()); err != nil {
		t.Errorf("SchemaReady = %v", err)
	}
	if owners, err := st.Owners(t.Context(), store.Scope{All: true, Owners: []string{}}); err != nil || len(owners) != 0 {
		t.Errorf("Owners on an empty store = %v, %v", owners, err)
	}
}
