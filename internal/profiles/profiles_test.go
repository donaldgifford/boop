package profiles_test

import (
	"slices"
	"testing"

	"github.com/donaldgifford/boop/internal/profiles"
)

// exampleRules are the design's example rules.
var exampleRules = []profiles.Rule{
	{
		Profile:  "python",
		Extends:  []string{"github>boop-bot/renovate-config:python"},
		Managers: []string{"pip_requirements", "pip_setup", "pipenv", "poetry", "pep621", "pip-compile", "pyenv"},
	},
	{
		Profile:  "node",
		Extends:  []string{"github>boop-bot/renovate-config:node"},
		Managers: []string{"npm", "bun"},
	},
}

// The design's example: baseline < node < python < strict, no floor.
func exampleResolver(t *testing.T) *profiles.Resolver {
	t.Helper()
	r, err := profiles.New([]string{"baseline", "node", "python", "strict"}, exampleRules, "baseline", "strict", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestResolve(t *testing.T) {
	t.Parallel()
	const (
		python = "github>boop-bot/renovate-config:python"
		node   = "github>boop-bot/renovate-config:node"
		other  = "config:recommended"
	)
	tests := []struct {
		name     string
		extends  []string
		managers []string
		want     string
	}{
		{name: "nothing known", want: "strict"},
		{name: "nothing known, managers cannot loosen", managers: []string{"npm"}, want: "strict"},
		{name: "extends matches no rule", extends: []string{other}, want: "baseline"},
		{name: "python from extends", extends: []string{other, python}, want: "python"},
		{name: "python from managers", extends: []string{other}, managers: []string{"poetry"}, want: "python"},
		{name: "node from extends", extends: []string{node}, want: "node"},
		{name: "node tightened to python by managers", extends: []string{node}, managers: []string{"npm", "pip_requirements"}, want: "python"},
		{name: "python not loosened by node managers", extends: []string{python}, managers: []string{"npm"}, want: "python"},
		{name: "both presets, strictest wins", extends: []string{node, python}, want: "python"},
		{name: "preset with a suffix still matches", extends: []string{python + "#v2"}, want: "python"},
		{name: "unrelated managers", extends: []string{other}, managers: []string{"gomod", "dockerfile"}, want: "baseline"},
	}
	r := exampleResolver(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, raised := r.Resolve(tt.extends, tt.managers); got != tt.want || raised {
				t.Errorf("Resolve(%q, %q) = %q, %v; want %q, false", tt.extends, tt.managers, got, raised, tt.want)
			}
		})
	}
}

func TestNew_Rejects(t *testing.T) {
	t.Parallel()
	order := []string{"a", "b"}
	tests := []struct {
		name    string
		order   []string
		rules   []profiles.Rule
		def     string
		unknown string
		floor   *profiles.Floor
	}{
		{name: "empty order", def: "a", unknown: "a"},
		{name: "duplicate in order", order: []string{"a", "a"}, def: "a", unknown: "a"},
		{name: "unknown default", order: order, def: "c", unknown: "a"},
		{name: "unknown unknown", order: order, def: "a", unknown: "c"},
		{name: "unknown rule profile", order: order, rules: []profiles.Rule{{Profile: "c"}}, def: "a", unknown: "b"},
		{name: "floor without a sandboxed profile", order: order, def: "a", unknown: "b", floor: &profiles.Floor{Managers: []string{"poetry"}}},
		{name: "unknown sandboxed profile", order: order, def: "a", unknown: "b", floor: &profiles.Floor{Sandboxed: []string{"c"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := profiles.New(tt.order, tt.rules, tt.def, tt.unknown, tt.floor); err == nil {
				t.Errorf("New(%v, %v, %q, %q) err = nil, want error", tt.order, tt.rules, tt.def, tt.unknown)
			}
		})
	}
}

func TestStrictness(t *testing.T) {
	t.Parallel()
	r := exampleResolver(t)
	if got := r.Strictness("python"); got != 2 {
		t.Errorf("Strictness(python) = %d, want 2", got)
	}
	if got := r.Strictness("nope"); got != -1 {
		t.Errorf("Strictness(nope) = %d, want -1", got)
	}
}

// TestResolve_Floor is IMPL-0006 task 4.4. The order is
// baseline < node < jvm (sandboxed) < python (sandboxed) < strict.
func TestResolve_Floor(t *testing.T) {
	t.Parallel()
	const (
		python = "github>boop-bot/renovate-config:python"
		node   = "github>boop-bot/renovate-config:node"
		jvm    = "github>boop-bot/renovate-config:jvm"
		other  = "config:recommended"
	)
	order := []string{"baseline", "node", "jvm", "python", "strict"}
	rules := append([]profiles.Rule{{Profile: "jvm", Extends: []string{jvm}, Managers: []string{"gradle"}}}, exampleRules...)
	floor := []string{"bundler", "pip-compile", "poetry", "nuget"}
	build := func(t *testing.T, f *profiles.Floor) *profiles.Resolver {
		t.Helper()
		r, err := profiles.New(order, rules, "baseline", "node", f)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return r
	}
	withSandbox := build(t, &profiles.Floor{Managers: floor, Sandboxed: []string{"jvm", "python"}, Configured: true})
	noSandbox := build(t, &profiles.Floor{Managers: floor, Sandboxed: []string{"jvm", "python"}})
	added := build(t, &profiles.Floor{Managers: append(slices.Clone(floor), "gomod"), Sandboxed: []string{"jvm", "python"}, Configured: true})
	strictOnly := build(t, &profiles.Floor{Managers: floor, Sandboxed: []string{"jvm"}, Configured: true})

	tests := []struct {
		name       string
		r          *profiles.Resolver
		extends    []string
		managers   []string
		want       string
		wantRaised bool
	}{
		{name: "no floor manager", r: withSandbox, extends: []string{other}, managers: []string{"gomod", "npm"}, want: "node"},
		{
			name:       "floor manager under an ordinary profile",
			r:          withSandbox,
			extends:    []string{other},
			managers:   []string{"bundler"},
			want:       "jvm",
			wantRaised: true,
		},
		{
			name:       "floor manager under node",
			r:          withSandbox,
			extends:    []string{node},
			managers:   []string{"npm", "nuget"},
			want:       "jvm",
			wantRaised: true,
		},
		{name: "already sandboxed by rules", r: withSandbox, extends: []string{python}, managers: []string{"poetry"}, want: "python"},
		{name: "sandboxed by managers, not raised", r: withSandbox, extends: []string{other}, managers: []string{"gradle", "bundler"}, want: "jvm"},
		{
			name:     "above every sandboxed profile",
			r:        withSandbox,
			extends:  []string{other},
			managers: []string{"poetry", "strict-only"},
			want:     "python",
		},
		{
			name:       "ordinary profile above the sandbox",
			r:          strictOnly,
			extends:    []string{python},
			managers:   []string{"poetry"},
			want:       "jvm",
			wantRaised: true,
		},
		{name: "empty managers with a sandbox", r: withSandbox, extends: []string{other}, want: "jvm", wantRaised: true},
		{name: "empty managers without a sandbox", r: noSandbox, extends: []string{other}, want: "baseline"},
		{name: "floor manager without a sandbox", r: noSandbox, extends: []string{other}, managers: []string{"poetry"}, want: "python"},
		{
			name:       "floor manager without a sandbox, raised",
			r:          noSandbox,
			extends:    []string{other},
			managers:   []string{"bundler"},
			want:       "jvm",
			wantRaised: true,
		},
		{name: "floor_add", r: added, extends: []string{other}, managers: []string{"gomod"}, want: "jvm", wantRaised: true},
		{name: "floor_add absent", r: withSandbox, extends: []string{other}, managers: []string{"gomod"}, want: "baseline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, raised := tt.r.Resolve(tt.extends, tt.managers)
			if got != tt.want || raised != tt.wantRaised {
				t.Errorf("Resolve(%q, %q) = %q, %v; want %q, %v", tt.extends, tt.managers, got, raised, tt.want, tt.wantRaised)
			}
		})
	}
	if !withSandbox.Sandboxed("jvm") || withSandbox.Sandboxed("strict") {
		t.Error("Sandboxed(jvm) = false or Sandboxed(strict) = true")
	}
}
