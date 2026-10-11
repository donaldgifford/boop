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

// Package config loads boopd's config file (ADR-0004): one HCL file,
// shipped with the chart, decoded with hclkit (IMPL-0001 OQ2) into a
// typed, validated Config.
//
// Load reads the file once, decodes it block for block (unknown
// attributes and blocks are errors), resolves profile inheritance,
// applies the defaults and runs every validation rule, returning every
// diagnostic rather than the first, each with its file, line and column.
// Secret-backed values are not read by Load; ReadSecrets does that at
// worker start, so `boopd config validate` runs without secrets mounted.
//
// A Config is read-only once built. It never travels as a workflow
// input or result: it carries secrets.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/donaldgifford/boop/internal/jobspec"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/profiles"
	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/hclkit/pkg/hclkit"
	"github.com/donaldgifford/hclkit/pkg/hclkit/ctytypes"
	"github.com/donaldgifford/hclkit/pkg/hclkit/funcs"
)

// Defaults (DESIGN-0001 § API / Interface Changes, OQ4, OQ10).
const (
	DefaultSecretsDir     = "/etc/boopd/secrets" //nolint:gosec // G101: a mount path, not a credential
	DefaultEndpoint       = "https://api.github.com"
	DefaultLogLevel       = "info"
	DefaultPendingTimeout = 10 * time.Minute
	DefaultCadence        = 24 * time.Hour
	DefaultDiscoveryEvery = 6 * time.Hour
)

// Probe modes for discovery.probe (DESIGN-0001 OQ3).
const (
	ProbeGraphQL = "graphql"
	ProbeREST    = "rest"
)

var (
	probeEnum    = ctytypes.Enum("discovery.probe", []string{ProbeGraphQL, ProbeREST})
	dryRunEnum   = ctytypes.Enum("renovate.dry_run", []string{"extract", "lookup", "full"})
	logLevelEnum = ctytypes.Enum("renovate.log_level", []string{"trace", "debug", "info", "warn", "error", "fatal"})
)

// Config is the decoded, validated config file.
type Config struct {
	// SecretsDir is where the chart mounts the Secrets named by refs:
	// <SecretsDir>/<name>/<key>.
	SecretsDir string
	Renovate   Renovate
	Runs       Runs
	// Profiles are keyed by name with inheritance already resolved.
	Profiles map[string]*Profile
	// Order lists every profile, ascending strictness.
	Order          []string
	DefaultProfile string
	UnknownProfile string
	ProfileRules   []ProfileRule
	Apps           []*App
}

// Renovate is the renovate block: what every run shares.
type Renovate struct {
	// Image is the Renovate image, pinned by digest.
	Image string
	// ConfigPath is the one config file path discovery probes.
	ConfigPath   string
	SharedPreset string
	LogLevel     string
	// DryRun is "", "extract", "lookup" or "full".
	DryRun    string
	GitAuthor string
	// Global is merged into RENOVATE_CONFIG; an explicit false survives.
	Global map[string]any
	// RedisSecretRef names the Redis URL Secret, if any.
	RedisSecretRef *SecretRef
	// RedisURL is read from RedisSecretRef by ReadSecrets.
	RedisURL Secret
}

// SecretRef names one key of a Kubernetes Secret the chart mounts.
type SecretRef struct {
	Name string
	Key  string
}

// Runs is the runs block.
type Runs struct {
	// Namespace is where Jobs are created: the worker's own namespace.
	Namespace string
	// PendingTimeout bounds how long a run's pod may take to start.
	PendingTimeout time.Duration
	// MaxConcurrent caps runs across all installations (OQ12); 0 is no cap.
	MaxConcurrent int
}

// Profile is one ecosystem profile with inheritance resolved.
type Profile struct {
	Name     string
	Pod      jobspec.PodOverlay
	Renovate map[string]any
}

// ProfileRule maps presets and managers to a profile; every matching rule
// contributes and the strictest wins.
type ProfileRule struct {
	Extends  []string
	Managers []string
	Profile  string
}

// App is one configured GitHub App.
type App struct {
	Name string
	// Endpoint is the API base URL; https://api.github.com by default.
	Endpoint            string
	AppID               int64
	PrivateKeySecretRef SecretRef
	// Installations is the optional allowlist; empty means every
	// installation.
	Installations []int64
	// Cadence is how often each repository runs.
	Cadence   time.Duration
	Discovery Discovery
	Budget    Budget
	// PrivateKey is the App's PEM key, read from PrivateKeySecretRef by
	// ReadSecrets.
	PrivateKey Secret
}

// Discovery is an app's discovery block.
type Discovery struct {
	// Every is the Schedule interval and the RepoWorkflow absence base.
	Every        time.Duration
	Probe        string
	SkipForks    bool
	SkipArchived bool
}

// Budget is an app's budget block. Limits are discovered, never
// configured (ADR-0008).
type Budget struct {
	ReserveFraction   float64
	MaxConcurrentRuns int
	// DefaultEstimate is keyed by resource ("core", "graphql").
	DefaultEstimate map[string]int
}

// Resolver builds the profile resolver from the file's order, rules and
// fallbacks.
func (c *Config) Resolver() (*profiles.Resolver, error) {
	rules := make([]profiles.Rule, 0, len(c.ProfileRules))
	for _, r := range c.ProfileRules {
		rules = append(rules, profiles.Rule{Profile: r.Profile, Extends: r.Extends, Managers: r.Managers})
	}
	return profiles.New(c.Order, rules, c.DefaultProfile, c.UnknownProfile)
}

// Option tunes Load and Parse.
type Option func(*options)

type options struct {
	getenv func(string) string
}

// WithGetenv sets the lookup behind the env() function; os.Getenv by
// default.
func WithGetenv(getenv func(string) string) Option {
	return func(o *options) { o.getenv = getenv }
}

// Load reads and parses the config file at path.
func Load(path string, opts ...Option) (*Config, hclkit.Diagnostics) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, hclkit.NewDiagnostics(hcl.Diagnostics{{
			Severity: hcl.DiagError,
			Summary:  "Cannot read config file",
			Detail:   err.Error(),
		}}, nil)
	}
	return Parse(path, src, opts...)
}

// Parse decodes src as the config file named filename. Every diagnostic
// is returned; the Config is nil when any is an error.
func Parse(filename string, src []byte, opts ...Option) (*Config, hclkit.Diagnostics) {
	o := options{getenv: os.Getenv}
	for _, opt := range opts {
		opt(&o)
	}
	ctx := &hcl.EvalContext{Functions: map[string]function.Function{"env": funcs.Env(o.getenv)}}

	var raw fileSchema
	diags := hclkit.New(hclkit.WithEvalContext(ctx), hclkit.WithValidators(validators()...)).
		LoadBytes(filename, src, &raw)
	if diags.HasErrors() {
		return nil, diags
	}

	// A second parse of the same bytes under the same name gives the
	// file map that renders snippets for the diagnostics build adds.
	parser := hclparse.NewParser()
	if _, parseDiags := parser.ParseHCL(src, filename); parseDiags.HasErrors() {
		return nil, hclkit.NewDiagnostics(parseDiags, parser.Files())
	}

	b := &builder{ctx: ctx, filename: filename}
	cfg := b.build(&raw)
	b.validate(cfg, &raw)
	all := append(slices.Clone(diags.Diagnostics), b.diags...)
	if all.HasErrors() {
		return nil, hclkit.NewDiagnostics(all, parser.Files())
	}
	return cfg, hclkit.NewDiagnostics(all, parser.Files())
}

// builder turns the raw decode into a Config, collecting diagnostics.
type builder struct {
	ctx      *hcl.EvalContext
	filename string
	diags    hcl.Diagnostics
}

func (b *builder) errorf(subject hcl.Range, summary, format string, args ...any) {
	b.diags = append(b.diags, &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  summary,
		Detail:   fmt.Sprintf(format, args...),
		Subject:  subject.Ptr(),
	})
}

func (b *builder) build(raw *fileSchema) *Config {
	cfg := &Config{
		Renovate:       b.renovate(&raw.Renovate),
		Runs:           b.runs(&raw.Runs),
		Order:          b.stringList(raw.Order),
		DefaultProfile: raw.DefaultProfile,
		UnknownProfile: raw.UnknownProfile,
	}
	cfg.SecretsDir = derefOr(raw.SecretsDir, DefaultSecretsDir)
	cfg.Profiles = b.profiles(raw.Profiles)
	for i := range raw.ProfileRules {
		r := &raw.ProfileRules[i]
		cfg.ProfileRules = append(cfg.ProfileRules, ProfileRule{
			Extends:  slices.Clone(r.Extends),
			Managers: slices.Clone(r.Managers),
			Profile:  r.Profile,
		})
	}
	for i := range raw.Apps {
		cfg.Apps = append(cfg.Apps, b.app(&raw.Apps[i]))
	}
	return cfg
}

func (b *builder) renovate(r *renovateBlock) Renovate {
	out := Renovate{
		Image:     r.Image,
		LogLevel:  b.enum(logLevelEnum, r.LogLevel, DefaultLogLevel),
		DryRun:    b.enum(dryRunEnum, r.DryRun, ""),
		GitAuthor: deref(r.GitAuthor),
	}
	out.ConfigPath = derefOr(r.ConfigPath, platform.DefaultConfigPath)
	out.SharedPreset = deref(r.SharedPreset)
	if r.Global != nil {
		out.Global = b.options(r.Global, "global")
	}
	if r.RedisSecretRef != nil {
		out.RedisSecretRef = &SecretRef{Name: r.RedisSecretRef.Name, Key: r.RedisSecretRef.Key}
	}
	return out
}

func (b *builder) runs(r *runsBlock) Runs {
	out := Runs{
		Namespace:      r.Namespace,
		PendingTimeout: b.duration(r.PendingTimeout, DefaultPendingTimeout),
	}
	if r.MaxConcurrent != nil {
		out.MaxConcurrent = *r.MaxConcurrent
	}
	return out
}

func (b *builder) app(a *appBlock) *App {
	out := &App{
		Name:     a.Name,
		Endpoint: derefOr(a.Endpoint, DefaultEndpoint),
		AppID:    a.AppID,
		PrivateKeySecretRef: SecretRef{
			Name: a.PrivateKeySecretRef.Name,
			Key:  a.PrivateKeySecretRef.Key,
		},
		Installations: b.intList(a.Installations),
		Cadence:       b.duration(a.Cadence, DefaultCadence),
	}
	d := a.Discovery
	if d == nil {
		d = &discoveryBlock{}
	}
	out.Discovery = Discovery{
		Every:        b.duration(d.Every, DefaultDiscoveryEvery),
		Probe:        b.enum(probeEnum, d.Probe, ProbeGraphQL),
		SkipForks:    derefOr(d.SkipForks, true),
		SkipArchived: derefOr(d.SkipArchived, true),
	}
	bg := a.Budget
	if bg == nil {
		bg = &budgetBlock{}
	}
	out.Budget = Budget{
		ReserveFraction:   derefOr(bg.ReserveFraction, workflows.DefaultReserveFraction),
		MaxConcurrentRuns: derefOr(bg.MaxConcurrentRuns, workflows.DefaultMaxConcurrentRuns),
		DefaultEstimate: mergeMap(map[string]int{
			workflows.ResourceCore:    workflows.DefaultEstimateCore,
			workflows.ResourceGraphQL: workflows.DefaultEstimateGraphQL,
		}, bg.DefaultEstimate),
	}
	return out
}

// duration evaluates an optional duration attribute: def when absent.
// A set value must be positive; "0s" is not a way to ask for the default.
func (b *builder) duration(expr hcl.Expression, def time.Duration) time.Duration {
	if isNull(expr, b.ctx) {
		return def
	}
	d, diags := ctytypes.DecodeDuration(expr, b.ctx)
	b.diags = append(b.diags, diags...)
	if !diags.HasErrors() && d <= 0 {
		b.errorf(expr.Range(), "Invalid duration", "The duration must be positive; omit the attribute for the default.")
	}
	return d
}

// enum evaluates an optional closed-set attribute: def when absent.
func (b *builder) enum(e ctytypes.EnumType, expr hcl.Expression, def string) string {
	if isNull(expr, b.ctx) {
		return def
	}
	s, diags := e.DecodeExpr(expr, b.ctx)
	b.diags = append(b.diags, diags...)
	return s
}

// stringList evaluates a list of strings element by element.
func (b *builder) stringList(expr hcl.Expression) []string {
	vals := b.listValues(expr, cty.String)
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, v.val.AsString())
	}
	return out
}

// intList evaluates a list of positive whole numbers (installation ids)
// element by element.
func (b *builder) intList(expr hcl.Expression) []int64 {
	elems := b.listValues(expr, cty.Number)
	out := make([]int64, 0, len(elems))
	for _, e := range elems {
		bf := e.val.AsBigFloat()
		n, acc := bf.Int64()
		if !bf.IsInt() || acc != 0 || n <= 0 {
			b.errorf(e.rng, "Invalid installation id", "Installation ids are positive whole numbers.")
			continue
		}
		out = append(out, n)
	}
	return out
}

type listElem struct {
	val cty.Value
	rng hcl.Range
}

func (b *builder) listValues(expr hcl.Expression, want cty.Type) []listElem {
	if isNull(expr, b.ctx) {
		return nil
	}
	elems, diags := hcl.ExprList(expr)
	if diags.HasErrors() {
		b.diags = append(b.diags, diags...)
		return nil
	}
	out := make([]listElem, 0, len(elems))
	for _, e := range elems {
		v, vd := e.Value(b.ctx)
		b.diags = append(b.diags, vd...)
		if vd.HasErrors() {
			continue
		}
		if v.IsNull() || !v.Type().Equals(want) {
			b.errorf(e.Range(), "Invalid list element", "Each element must be a %s.", want.FriendlyName())
			continue
		}
		out = append(out, listElem{val: v, rng: e.Range()})
	}
	return out
}

// options converts a block of Renovate options to JSON-shaped values.
func (b *builder) options(fb *freeBlock, where string) map[string]any {
	attrs, diags := fb.Body.JustAttributes()
	b.diags = append(b.diags, diags...)
	out := make(map[string]any, len(attrs))
	for name, attr := range attrs {
		v, vd := attr.Expr.Value(b.ctx)
		b.diags = append(b.diags, vd...)
		if vd.HasErrors() {
			continue
		}
		if v.IsNull() {
			b.errorf(attr.Expr.Range(), "Invalid Renovate option", "%s.%s must not be null.", where, name)
			continue
		}
		val, err := ctyToAny(v)
		if err != nil {
			b.errorf(attr.Expr.Range(), "Invalid Renovate option", "%s.%s: %v.", where, name, err)
			continue
		}
		if err := jobspec.ValidateOptions(where, map[string]any{name: val}); err != nil {
			b.errorf(attr.NameRange, "Forbidden Renovate option", "%v.", err)
			continue
		}
		out[name] = val
	}
	return out
}

func ctyToAny(v cty.Value) (any, error) {
	raw, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func isNull(expr hcl.Expression, ctx *hcl.EvalContext) bool {
	if expr == nil {
		return true
	}
	v, diags := expr.Value(ctx)
	return !diags.HasErrors() && v.IsNull()
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func derefOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}
