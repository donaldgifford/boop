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

// Copied from repo-guardian internal/api/cursor.go at a72faf9; only the
// design reference changed (DESIGN-0004 § The API).

package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// maxCursorLen bounds a cursor before it is decoded; real cursors are
// well under 300 bytes.
const maxCursorLen = 1024

// errCursor is the 400 answer for a malformed cursor.
var errCursor = errors.New("cursor is malformed")

// errCursorFilters is the 400 answer for a cursor reused with different
// filters (DESIGN-0004 § The API): paging on under changed filters would
// silently skip or repeat rows.
var errCursorFilters = errors.New("cursor was issued for different filters; restart without it")

// cursor is the wire form of a keyset cursor: base64url JSON carrying
// the last row's sort key (which ends with its ID) and a hash of the
// filters that produced the page.
type cursor struct {
	Key     json.RawMessage `json:"k"`
	Filters string          `json:"f"`
}

// filterHash hashes a request's filters. filters must marshal
// deterministically: a struct, never a map.
func filterHash(filters any) (string, error) {
	b, err := json.Marshal(filters)
	if err != nil {
		return "", fmt.Errorf("hash cursor filters: %w", err)
	}

	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:8]), nil
}

// encodeCursor returns the cursor for the page after key under filters.
func encodeCursor(key, filters any) (string, error) {
	k, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("encode cursor key: %w", err)
	}

	h, err := filterHash(filters)
	if err != nil {
		return "", err
	}

	b, err := json.Marshal(cursor{Key: k, Filters: h})
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeCursor decodes s into key, and fails with errCursorFilters when
// s was issued under different filters. An absent cursor (nil or empty)
// leaves key untouched and reports false.
func decodeCursor(s *string, key, filters any) (bool, error) {
	if s == nil || *s == "" {
		return false, nil
	}

	if len(*s) > maxCursorLen {
		return false, errCursor
	}

	b, err := base64.RawURLEncoding.DecodeString(*s)
	if err != nil {
		return false, errCursor
	}

	var c cursor
	if err := json.Unmarshal(b, &c); err != nil || len(c.Key) == 0 {
		return false, errCursor
	}

	h, err := filterHash(filters)
	if err != nil {
		return false, err
	}

	if c.Filters != h {
		return false, errCursorFilters
	}

	if err := json.Unmarshal(c.Key, key); err != nil {
		return false, errCursor
	}

	return true, nil
}
