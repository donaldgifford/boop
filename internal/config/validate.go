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

package config

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/donaldgifford/boop/internal/jobspec"
	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/hclkit/pkg/hclkit"
	"github.com/donaldgifford/hclkit/pkg/hclkit/validate"
)

// maxReserveFraction bounds budget.reserve_fraction: holding back more
// than half the hourly budget would starve runs.
const maxReserveFraction = 0.5

// digestPinned matches an image reference pinned by sha256 digest.
var digestPinned = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)

// validators are the cross-block checks hclkit runs during decode: every
// profile reference names a declared profile block, and no two apps share
// an App id.
func validators() []hclkit.Validator {
	const kind = "profile"
	return []hclkit.Validator{
		validate.NewRefValidator("order", kind),
		validate.NewRefValidator("default_profile", kind),
		validate.NewRefValidator("unknown_profile", kind),
		validate.NewRefValidator("profile", kind), // profile_rule's target
		validate.NewRefValidator("inherit", kind),
		validate.NewUniqueValidator("app", "app_id"),
	}
}

// validate runs the rules that need the decoded values (IMPL-0001 task
// 3.2). Every failure is a diagnostic at the offending range; none stops
// the others.
func (b *builder) validate(cfg *Config, raw *fileSchema) {
	b.validateRenovate(&cfg.Renovate, &raw.Renovate)
	b.validateRuns(&raw.Runs)
	b.validateOrder(cfg, raw)
	for i := range raw.ProfileRules {
		r := &raw.ProfileRules[i]
		if len(r.Extends) == 0 && len(r.Managers) == 0 {
			b.errorf(r.DefRange, "Empty profile rule", "A profile_rule needs extends, managers or both.")
		}
	}
	for i := range raw.Profiles {
		if pod := raw.Profiles[i].Pod; pod != nil && pod.TerminationGrace != nil && *pod.TerminationGrace < 0 {
			b.errorf(pod.TerminationRange, "Invalid grace period", "termination_grace_period_seconds must not be negative.")
		}
	}
	b.validateApps(raw.Apps)
}

func (b *builder) validateRenovate(r *Renovate, raw *renovateBlock) {
	if !digestPinned.MatchString(r.Image) {
		b.errorf(raw.ImageRange, "Image not pinned", "renovate.image %q must be pinned by digest (name:tag@sha256:<64 hex>).", r.Image)
	}
	if err := checkRelativeFile(r.ConfigPath); err != nil {
		b.errorf(rangeOr(raw.ConfigPathRange, raw.DefRange), "Invalid config_path", "%v.", err)
	}
	if err := jobspec.ValidatePreset(r.SharedPreset); err != nil {
		b.errorf(rangeOr(raw.SharedPresetRange, raw.DefRange), "Invalid shared_preset", "%v.", err)
	}
	if ref := raw.RedisSecretRef; ref != nil {
		b.validateSecretRef(ref, "redis_secret_ref")
	}
}

func (b *builder) validateRuns(raw *runsBlock) {
	if errs := validation.IsDNS1123Label(raw.Namespace); len(errs) > 0 {
		b.errorf(raw.NamespaceRange, "Invalid namespace", "runs.namespace %q: %s.", raw.Namespace, strings.Join(errs, "; "))
	}
	if raw.MaxConcurrent != nil && *raw.MaxConcurrent < 0 {
		b.errorf(raw.MaxConcurrentRange, "Invalid max_concurrent", "runs.max_concurrent must not be negative; 0 means no cap.")
	}
}

// validateOrder checks that order names every profile exactly once.
// The reference validator has already checked that each name exists.
func (b *builder) validateOrder(cfg *Config, raw *fileSchema) {
	elems, diags := hcl.ExprList(raw.Order)
	if diags.HasErrors() {
		return // decode reported the shape
	}
	seen := make(map[string]hcl.Range, len(elems))
	for _, e := range elems {
		v, vd := e.Value(b.ctx)
		if vd.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			continue
		}
		name := v.AsString()
		if first, dup := seen[name]; dup {
			b.errorf(e.Range(), "Duplicate profile in order", "Profile %q is already listed at %s.", name, first)
			continue
		}
		seen[name] = e.Range()
	}
	var missing []string
	for name := range cfg.Profiles {
		if _, ok := seen[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		b.errorf(raw.Order.Range(), "Profile missing from order",
			"order must list every profile exactly once; missing: %s.", strings.Join(missing, ", "))
	}
}

func (b *builder) validateApps(apps []appBlock) {
	if len(apps) == 0 {
		b.errorf(hcl.Range{Filename: b.filename, Start: hcl.InitialPos, End: hcl.InitialPos},
			"No app configured", "At least one app block is required.")
	}
	names := make(map[string]hcl.Range, len(apps))
	for i := range apps {
		a := &apps[i]
		if first, dup := names[a.Name]; dup {
			b.errorf(a.DefRange, "Duplicate app", "App %q is already defined at %s.", a.Name, first)
		}
		names[a.Name] = a.DefRange
		if a.AppID <= 0 {
			b.errorf(a.AppIDRange, "Invalid app_id", "app_id must be a positive GitHub App id.")
		}
		if a.Endpoint != nil {
			if u, err := url.Parse(*a.Endpoint); err != nil || u.Scheme != "https" || u.Host == "" {
				b.errorf(a.EndpointRange, "Invalid endpoint", "endpoint %q must be an https URL.", *a.Endpoint)
			}
		}
		b.validateSecretRef(&a.PrivateKeySecretRef, "private_key_secret_ref")
		if bg := a.Budget; bg != nil {
			b.validateBudget(bg)
		}
	}
}

func (b *builder) validateBudget(bg *budgetBlock) {
	if f := bg.ReserveFraction; f != nil && (*f < 0 || *f > maxReserveFraction) {
		b.errorf(bg.ReserveFractionRange, "Invalid reserve_fraction", "reserve_fraction must be in [0, %.1f].", maxReserveFraction)
	}
	if n := bg.MaxConcurrentRuns; n != nil && *n < 0 {
		b.errorf(bg.MaxConcurrentRunsRange, "Invalid max_concurrent_runs", "max_concurrent_runs must not be negative; 0 means budget only.")
	}
	keys := make([]string, 0, len(bg.DefaultEstimate))
	for k := range bg.DefaultEstimate {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		switch {
		case !slices.Contains(workflows.TrackedResources, k):
			b.errorf(bg.DefaultEstimateRange, "Invalid default_estimate",
				"Unknown resource %q; the budget tracks %s.", k, strings.Join(workflows.TrackedResources, " and "))
		case bg.DefaultEstimate[k] <= 0:
			b.errorf(bg.DefaultEstimateRange, "Invalid default_estimate", "default_estimate.%s must be positive.", k)
		}
	}
}

func (b *builder) validateSecretRef(ref *secretRefBlock, what string) {
	if strings.TrimSpace(ref.Name) == "" {
		b.errorf(rangeOr(ref.NameRange, ref.DefRange), "Invalid secret ref", "%s.name must not be empty.", what)
	} else if errs := validation.IsDNS1123Subdomain(ref.Name); len(errs) > 0 {
		b.errorf(ref.NameRange, "Invalid secret ref", "%s.name %q: %s.", what, ref.Name, strings.Join(errs, "; "))
	}
	if strings.TrimSpace(ref.Key) == "" {
		b.errorf(rangeOr(ref.KeyRange, ref.DefRange), "Invalid secret ref", "%s.key must not be empty.", what)
	} else if errs := validation.IsConfigMapKey(ref.Key); len(errs) > 0 {
		b.errorf(ref.KeyRange, "Invalid secret ref", "%s.key %q: %s.", what, ref.Key, strings.Join(errs, "; "))
	}
}

// checkRelativeFile accepts a clean, relative path to a file inside the
// repository.
func checkRelativeFile(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("config_path must not be empty")
	case path.IsAbs(p):
		return fmt.Errorf("config_path %q must be relative to the repository root", p)
	case strings.HasSuffix(p, "/"), path.Clean(p) != p:
		return fmt.Errorf("config_path %q must be a clean file path", p)
	case p == "." || p == ".." || strings.HasPrefix(p, "../"):
		return fmt.Errorf("config_path %q must stay inside the repository", p)
	}
	return nil
}

// rangeOr returns r unless it is unset (an absent optional attribute),
// in which case it returns fallback.
func rangeOr(r, fallback hcl.Range) hcl.Range {
	if r.Filename == "" {
		return fallback
	}
	return r
}
