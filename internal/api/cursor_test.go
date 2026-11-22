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

// Copied from repo-guardian internal/api/cursor_test.go at a72faf9.
// Changes: the filter field is owner, not org.

package api

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

type testFilters struct {
	Owner  string `json:"owner"`
	Status string `json:"status"`
}

type testKey struct {
	At time.Time `json:"at"`
	ID int64     `json:"id"`
}

func TestCursor_RoundTrip(t *testing.T) {
	t.Parallel()

	want := testKey{At: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), ID: 42}
	filters := testFilters{Owner: "acme", Status: "open"}

	s, err := encodeCursor(want, filters)
	if err != nil {
		t.Fatal(err)
	}

	var got testKey

	ok, err := decodeCursor(&s, &got, filters)
	if err != nil || !ok {
		t.Fatalf("decodeCursor = %v, %v", ok, err)
	}

	if !got.At.Equal(want.At) || got.ID != want.ID {
		t.Errorf("key = %+v, want %+v", got, want)
	}
}

func TestCursor_Refusals(t *testing.T) {
	t.Parallel()

	filters := testFilters{Owner: "acme"}

	valid, err := encodeCursor(testKey{ID: 1}, filters)
	if err != nil {
		t.Fatal(err)
	}

	long := string(make([]byte, maxCursorLen+1))

	tests := []struct {
		name    string
		cursor  string
		filters testFilters
		want    error
	}{
		{name: "different filters", cursor: valid, filters: testFilters{Owner: "other"}, want: errCursorFilters},
		{name: "not base64", cursor: "!!!", filters: filters, want: errCursor},
		{name: "not json", cursor: base64.RawURLEncoding.EncodeToString([]byte("nope")), filters: filters, want: errCursor},
		{name: "no key", cursor: base64.RawURLEncoding.EncodeToString([]byte(`{"f":"x"}`)), filters: filters, want: errCursor},
		{name: "too long", cursor: long, filters: filters, want: errCursor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var key testKey

			if _, err := decodeCursor(&tt.cursor, &key, tt.filters); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCursor_AbsentIsFirstPage(t *testing.T) {
	t.Parallel()

	empty := ""

	for _, s := range []*string{nil, &empty} {
		var key testKey

		ok, err := decodeCursor(s, &key, testFilters{})
		if ok || err != nil {
			t.Errorf("decodeCursor(%v) = %v, %v; want false, nil", s, ok, err)
		}
	}
}
