package profiles_test

import (
	"testing"

	"github.com/donaldgifford/boop/internal/profiles"
)

// The design's example: baseline < node < python < strict.
func exampleResolver(t *testing.T) *profiles.Resolver {
	t.Helper()
	r, err := profiles.New(
		[]string{"baseline", "node", "python", "strict"},
		[]profiles.Rule{
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
		},
		"baseline", "strict",
	)
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
			if got := r.Resolve(tt.extends, tt.managers); got != tt.want {
				t.Errorf("Resolve(%q, %q) = %q, want %q", tt.extends, tt.managers, got, tt.want)
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
	}{
		{name: "empty order", def: "a", unknown: "a"},
		{name: "duplicate in order", order: []string{"a", "a"}, def: "a", unknown: "a"},
		{name: "unknown default", order: order, def: "c", unknown: "a"},
		{name: "unknown unknown", order: order, def: "a", unknown: "c"},
		{name: "unknown rule profile", order: order, rules: []profiles.Rule{{Profile: "c"}}, def: "a", unknown: "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := profiles.New(tt.order, tt.rules, tt.def, tt.unknown); err == nil {
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
