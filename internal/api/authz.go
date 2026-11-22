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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/boop/internal/store"
)

// allOwners is the authz wildcard: a group mapped to it sees every owner.
const allOwners = "*"

// AuthzConfig maps identities to visible owners and to actions
// (DESIGN-0004 § Authentication and authorisation, OQ8). It is the
// api.authz_file, read once at start; the chart's checksum annotation
// rolls the pods on change.
//
//	owners:                      # group -> visible owners ("*" = all)
//	  platform-engineering: ["*"]
//	  team-web: ["acme-web"]
//	operators:                   # groups allowed to POST actions
//	  - platform-engineering
//	clients:                     # azp -> groups, for machine clients
//	  wiz-correlator: ["platform-engineering"]
type AuthzConfig struct {
	Owners    map[string][]string `yaml:"owners"`
	Operators []string            `yaml:"operators"`
	Clients   map[string][]string `yaml:"clients"`
}

// LoadAuthzConfig reads and validates the authz file at path: unknown
// keys are refused, and a client may map only to a group the file
// defines (under owners or operators).
func LoadAuthzConfig(path string) (*AuthzConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read authz file: %w", err)
	}
	var cfg AuthzConfig
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse authz file %s: %w", path, err)
	}
	for azp, groups := range cfg.Clients {
		for _, g := range groups {
			if _, ok := cfg.Owners[g]; !ok && !slices.Contains(cfg.Operators, g) {
				return nil, fmt.Errorf("authz file %s: client %q maps to undefined group %q", path, azp, g)
			}
		}
	}
	return &cfg, nil
}

// groups is the caller's groups plus its client's mapped groups.
func (c *AuthzConfig) groups(claims *Claims) []string {
	groups := slices.Clone(claims.Groups)
	if claims.Client != "" {
		groups = append(groups, c.Clients[claims.Client]...)
	}
	return groups
}

// Scope resolves the owners claims may see: the union over the caller's
// groups. A caller in no mapped group sees nothing.
func (c *AuthzConfig) Scope(claims *Claims) Scope {
	var owners []string
	for _, g := range c.groups(claims) {
		for _, o := range c.Owners[g] {
			if o == allOwners {
				return ScopeAll()
			}
			owners = append(owners, o)
		}
	}
	return ScopeOwners(owners...)
}

// Operator reports whether claims carry an operator group.
func (c *AuthzConfig) Operator(claims *Claims) bool {
	return slices.ContainsFunc(c.groups(claims), func(g string) bool { return slices.Contains(c.Operators, g) })
}

// Scope is the set of owners a caller may see. The zero Scope sees
// nothing. Owners are GitHub logins, compared case-insensitively.
type Scope struct {
	all    bool
	owners []string
}

// ScopeAll sees every owner.
func ScopeAll() Scope { return Scope{all: true} }

// ScopeOwners sees the named owners.
func ScopeOwners(owners ...string) Scope {
	out := make([]string, 0, len(owners))
	for _, o := range owners {
		out = append(out, strings.ToLower(o))
	}
	slices.Sort(out)
	return Scope{owners: slices.Compact(out)}
}

// All reports whether the scope is every owner.
func (s Scope) All() bool { return s.all }

// Owners is the named owners, lowercased and sorted; nil for All.
func (s Scope) Owners() []string { return slices.Clone(s.owners) }

// Empty reports whether the scope sees nothing.
func (s Scope) Empty() bool { return !s.all && len(s.owners) == 0 }

// Sees reports whether owner is visible.
func (s Scope) Sees(owner string) bool {
	return s.all || slices.Contains(s.owners, strings.ToLower(owner))
}

// Narrow is the scope a request's owner filter leaves: the scope itself
// without one, the one owner when it is visible, and errNotFound (404,
// like an unknown owner) when it is not.
func (s Scope) Narrow(owner *string) (Scope, error) {
	if owner == nil || *owner == "" {
		return s, nil
	}
	if !s.Sees(*owner) {
		return Scope{}, errNotFound
	}
	return ScopeOwners(*owner), nil
}

// Store is the scope as the store's reads take it: every scoped query
// filters `(@all_owners::boolean OR lower(owner) = ANY(@owners::text[]))`.
func (s Scope) Store() store.Scope {
	owners := s.Owners()
	if owners == nil {
		owners = []string{}
	}
	return store.Scope{All: s.all, Owners: owners}
}
