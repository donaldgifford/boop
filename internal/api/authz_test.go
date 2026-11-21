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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeAuthz(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAuthzConfig_Refusals(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"unknown field":   "owners: {}\nrogue: 1\n",
		"undefined group": "owners: {a: [x]}\nclients: {bot: [b]}\n",
		"not yaml":        "owners: [\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadAuthzConfig(writeAuthz(t, body)); err == nil {
				t.Error("LoadAuthzConfig = nil error")
			}
		})
	}
}

func TestLoadAuthzConfig_Valid(t *testing.T) {
	t.Parallel()
	cfg, err := LoadAuthzConfig(writeAuthz(t, `owners:
  platform: ["*"]
  team-web: [acme-web]
operators: [platform, oncall]
clients:
  wiz: [platform]
  pager: [oncall]
`))
	if err != nil {
		t.Fatalf("LoadAuthzConfig = %v", err)
	}
	if !cfg.Scope(&Claims{Client: "wiz"}).All() {
		t.Error("client wiz does not see every owner")
	}
	if !cfg.Operator(&Claims{Client: "pager"}) {
		t.Error("client pager mapped to an operators-only group is no operator")
	}
	if _, err := LoadAuthzConfig(writeAuthz(t, "")); err != nil {
		t.Errorf("LoadAuthzConfig(empty) = %v, want nil", err)
	}
}

func TestAuthzConfig_Scope(t *testing.T) {
	t.Parallel()
	cfg := &AuthzConfig{
		Owners:  map[string][]string{"web": {"Acme-Web"}, "docs": {"acme-docs", "acme-web"}, "admin": {"*"}},
		Clients: map[string][]string{"bot": {"docs"}},
	}
	tests := []struct {
		claims  Claims
		wantAll bool
		want    string
	}{
		{claims: Claims{Groups: []string{"web", "docs"}}, want: "acme-docs,acme-web"},
		{claims: Claims{Groups: []string{"admin", "web"}}, wantAll: true},
		{claims: Claims{Groups: []string{"nobody"}}},
		{claims: Claims{}},
		{claims: Claims{Client: "bot"}, want: "acme-docs,acme-web"},
		{claims: Claims{Client: "unknown", Groups: []string{"web"}}, want: "acme-web"},
	}
	for _, tt := range tests {
		s := cfg.Scope(&tt.claims)
		if s.All() != tt.wantAll || strings.Join(s.Owners(), ",") != tt.want {
			t.Errorf("Scope(%+v) = all %v owners %v, want all %v owners %q", tt.claims, s.All(), s.Owners(), tt.wantAll, tt.want)
		}
		if s.Empty() != (!tt.wantAll && tt.want == "") {
			t.Errorf("Scope(%+v).Empty() = %v", tt.claims, s.Empty())
		}
	}
}

func TestAuthzConfig_Operator(t *testing.T) {
	t.Parallel()
	cfg := &AuthzConfig{
		Owners:    map[string][]string{"platform": {"*"}, "web": {"acme-web"}},
		Operators: []string{"platform"},
		Clients:   map[string][]string{"ci": {"platform"}},
	}
	tests := []struct {
		claims Claims
		want   bool
	}{
		{claims: Claims{Groups: []string{"platform"}}, want: true},
		{claims: Claims{Groups: []string{"web"}}},
		{claims: Claims{}},
		{claims: Claims{Client: "ci"}, want: true},
	}
	for _, tt := range tests {
		if got := cfg.Operator(&tt.claims); got != tt.want {
			t.Errorf("Operator(%+v) = %v, want %v", tt.claims, got, tt.want)
		}
	}
}

func TestScope_Narrow(t *testing.T) {
	t.Parallel()
	ptr := func(s string) *string { return &s }
	some := ScopeOwners("acme-web", "acme-docs")
	tests := []struct {
		name    string
		scope   Scope
		owner   *string
		want    string
		wantAll bool
		wantErr error
	}{
		{name: "no filter keeps scope", scope: some, want: "acme-docs,acme-web"},
		{name: "empty filter keeps scope", scope: some, owner: ptr(""), want: "acme-docs,acme-web"},
		{name: "visible owner", scope: some, owner: ptr("ACME-WEB"), want: "acme-web"},
		{name: "invisible owner is 404", scope: some, owner: ptr("other"), wantErr: errNotFound},
		{name: "all narrows to one", scope: ScopeAll(), owner: ptr("other"), want: "other"},
		{name: "all without filter", scope: ScopeAll(), wantAll: true},
		{name: "nothing visible is 404", scope: Scope{}, owner: ptr("acme-web"), wantErr: errNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.scope.Narrow(tt.owner)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Narrow = %v, want %v", err, tt.wantErr)
			}
			if got.All() != tt.wantAll || strings.Join(got.Owners(), ",") != tt.want {
				t.Errorf("Narrow = all %v owners %v, want all %v owners %q", got.All(), got.Owners(), tt.wantAll, tt.want)
			}
		})
	}
}

func TestScope_Store(t *testing.T) {
	t.Parallel()
	if s := ScopeAll().Store(); !s.All || s.Owners == nil {
		t.Errorf("ScopeAll().Store() = %+v, want All and a non-nil owners array", s)
	}
	if s := (Scope{}).Store(); s.All || s.Owners == nil || len(s.Owners) != 0 {
		t.Errorf("Scope{}.Store() = %+v, want nothing visible and a non-nil owners array", s)
	}
	if s := ScopeOwners("B", "a", "b").Store(); !slices.Equal(s.Owners, []string{"a", "b"}) {
		t.Errorf("ScopeOwners(B, a, b).Store().Owners = %v, want [a b]", s.Owners)
	}
}
