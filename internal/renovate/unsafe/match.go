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

package unsafe

import (
	"regexp"
	"slices"
	"strings"
	"sync"
)

// MatchFloor returns the managers among floor whose default
// managerFilePatterns match any of paths, sorted (IMPL-0006 task 7.2).
// floor is Floor() plus any configured additions. A manager with no
// static patterns, or an empty list (pip-compile), never matches: only
// the repository's own config enables it, and an ordinary run in enforce
// mode forces it off.
func MatchFloor(floor, paths []string) []string {
	var out []string
	for _, m := range floor {
		for _, re := range compiled(m) {
			if slices.ContainsFunc(paths, re.MatchString) {
				out = append(out, m)
				break
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

var (
	compiledMu sync.Mutex
	compiledBy = map[string][]*regexp.Regexp{}
)

// compiled returns manager's default patterns as Go regexps, compiled
// once. A pattern that does not compile is skipped: Renovate's
// JavaScript regexps are RE2-compatible for every floor manager, which
// the unsafe tests check.
func compiled(manager string) []*regexp.Regexp {
	compiledMu.Lock()
	defer compiledMu.Unlock()
	if res, ok := compiledBy[manager]; ok {
		return res
	}
	var res []*regexp.Regexp
	for _, p := range defaultFilePatterns[manager] {
		if re, err := compilePattern(p); err == nil {
			res = append(res, re)
		}
	}
	compiledBy[manager] = res
	return res
}

// compilePattern turns one managerFilePatterns entry into a regexp.
// Renovate reads "/.../" as a regular expression and anything else as a
// minimatch glob; the glob form here covers *, ** and ?.
func compilePattern(p string) (*regexp.Regexp, error) {
	if len(p) >= 2 && strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/") {
		return regexp.Compile(p[1 : len(p)-1])
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
