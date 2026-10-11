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

// Package profiles picks the ecosystem profile for a run (DESIGN-0001
// § Ecosystem profiles, OQ1, OQ9). It is a leaf: the config package
// builds a Resolver from the file, and activities call Resolve with what
// discovery and the last report learned.
package profiles

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Rule maps presets and managers to a profile. Every matching rule
// contributes; the strictest wins.
type Rule struct {
	// Profile is the profile the rule selects.
	Profile string
	// Extends are preset substrings matched against each extends entry.
	Extends []string
	// Managers are Renovate manager names matched exactly.
	Managers []string
}

// Resolver resolves a repository's profile. It is immutable and safe for
// concurrent use.
type Resolver struct {
	rank           map[string]int
	rules          []Rule
	defaultProfile string
	unknownProfile string
}

// New builds a Resolver. order lists every profile in ascending
// strictness; every other name must appear in it.
func New(order []string, rules []Rule, defaultProfile, unknownProfile string) (*Resolver, error) {
	if len(order) == 0 {
		return nil, errors.New("profiles: order is empty")
	}
	rank := make(map[string]int, len(order))
	for i, name := range order {
		if _, dup := rank[name]; dup {
			return nil, fmt.Errorf("profiles: %q appears twice in order", name)
		}
		rank[name] = i
	}
	check := func(what, name string) error {
		if _, ok := rank[name]; !ok {
			return fmt.Errorf("profiles: %s %q is not in order", what, name)
		}
		return nil
	}
	if err := errors.Join(check("default profile", defaultProfile), check("unknown profile", unknownProfile)); err != nil {
		return nil, err
	}
	cloned := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if err := check("rule profile", r.Profile); err != nil {
			return nil, err
		}
		cloned = append(cloned, Rule{Profile: r.Profile, Extends: slices.Clone(r.Extends), Managers: slices.Clone(r.Managers)})
	}
	return &Resolver{rank: rank, rules: cloned, defaultProfile: defaultProfile, unknownProfile: unknownProfile}, nil
}

// Resolve returns the profile name for a repository.
//
// From extends: the strictest rule whose preset substring appears in an
// extends entry; the default profile when extends is known but matches
// no rule; the unknown profile when extends is empty. From managers: the
// strictest rule naming one of them. The result is the stricter of the
// two, so managers only ever tighten.
func (r *Resolver) Resolve(extends, managers []string) string {
	fromExtends := r.unknownProfile
	if len(extends) > 0 {
		fromExtends = r.defaultProfile
		for i := range r.rules {
			if matchesPreset(r.rules[i].Extends, extends) {
				fromExtends = r.stricter(fromExtends, r.rules[i].Profile)
			}
		}
	}
	out := fromExtends
	for i := range r.rules {
		if matchesManager(r.rules[i].Managers, managers) {
			out = r.stricter(out, r.rules[i].Profile)
		}
	}
	return out
}

// Strictness returns the profile's position in order, or -1.
func (r *Resolver) Strictness(profile string) int {
	if i, ok := r.rank[profile]; ok {
		return i
	}
	return -1
}

func (r *Resolver) stricter(a, b string) string {
	if r.rank[b] > r.rank[a] {
		return b
	}
	return a
}

func matchesPreset(patterns, extends []string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		for _, e := range extends {
			if strings.Contains(e, p) {
				return true
			}
		}
	}
	return false
}

func matchesManager(want, have []string) bool {
	for _, m := range want {
		if slices.Contains(have, m) {
			return true
		}
	}
	return false
}
