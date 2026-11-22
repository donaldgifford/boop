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

package api

import (
	"errors"
	"net/http"
	"strings"
)

// Page sizes (DESIGN-0004 § The API).
const (
	defaultLimit = 50
	maxLimit     = 200
)

// limitOf is the page size a request asks for; outside 1..maxLimit is
// 400.
func limitOf(l *int) (int32, error) {
	if l == nil {
		return defaultLimit, nil
	}
	if *l < 1 || *l > maxLimit {
		return 0, statusError(http.StatusBadRequest, "limit must be 1 to 200")
	}
	return int32(*l), nil
}

// afterKey decodes a request's cursor into key under filters; a
// malformed cursor or one issued for other filters is 400.
func afterKey(c *string, key, filters any) (bool, error) {
	ok, err := decodeCursor(c, key, filters)
	if errors.Is(err, errCursor) || errors.Is(err, errCursorFilters) {
		return false, statusError(http.StatusBadRequest, err.Error())
	}
	return ok, err
}

// paged trims rows read with limit+1 to limit and returns the cursor of
// the page after it, nil on the last page.
func paged[R, K any](rows []R, limit int32, key func(*R) K, filters any) ([]R, *string, error) {
	if len(rows) <= int(limit) {
		return rows, nil, nil
	}
	rows = rows[:limit]
	c, err := encodeCursor(key(&rows[len(rows)-1]), filters)
	if err != nil {
		return nil, nil, err
	}
	return rows, &c, nil
}

// purlMatch is a purl filter as the queries take it: exact for a purl
// with a version; otherwise base, the purl itself, and prefix, a LIKE
// pattern for every version of it.
type purlMatch struct {
	exact, base, prefix *string
}

func purlFilter(p *string) purlMatch {
	if p == nil || *p == "" {
		return purlMatch{}
	}
	if strings.Contains(*p, "@") {
		return purlMatch{exact: p}
	}
	base, prefix := *p, likeEscape(*p)+"@%"
	return purlMatch{base: &base, prefix: &prefix}
}

// likeEscape escapes LIKE's wildcards and its escape character.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

// slugPrefix is the q filter as a LIKE pattern on lower(slug).
func slugPrefix(q *string) *string {
	if q == nil || *q == "" {
		return nil
	}
	p := likeEscape(strings.ToLower(*q)) + "%"
	return &p
}

// nonEmpty is s, or nil when s is nil or empty.
func nonEmpty(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}
