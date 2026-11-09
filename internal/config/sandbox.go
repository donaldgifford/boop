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
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/donaldgifford/boop/internal/renovate/unsafe"
	"github.com/donaldgifford/hclkit/pkg/hclkit/ctytypes"
)

// Floor modes for sandbox.floor_mode (DESIGN-0005 OQ13).
const (
	// FloorReport runs a floor-raised repository as an ordinary Job and
	// logs that it would have been sandboxed.
	FloorReport = "report"
	// FloorEnforce sandboxes it, and forces the floor managers off in
	// ordinary runs.
	FloorEnforce = "enforce"
)

// Sandbox defaults (DESIGN-0005 § boopd.hcl, IMPL-0006 OQ9).
const (
	DefaultSandboxServiceAccount = "boopd-sandbox"
	// SandboxBootAllowance is added to runs.pending_timeout for the
	// sandbox's default: the VM boot.
	SandboxBootAllowance = 60 * time.Second
)

var floorModeEnum = ctytypes.Enum("sandbox.floor_mode", []string{FloorReport, FloorEnforce})

// GitHub App permission levels for sandbox.token_permissions.
const (
	PermissionRead  = "read"
	PermissionWrite = "write"
)

// DefaultTokenPermissions is the sandboxed run's token (IMPL-0006 OQ5 as
// amended 2026-11-09): what Renovate writes, and the two reads it needs.
func DefaultTokenPermissions() map[string]string {
	return map[string]string{
		"contents":             PermissionWrite,
		"pull_requests":        PermissionWrite,
		"issues":               PermissionWrite,
		"checks":               PermissionWrite,
		"statuses":             PermissionWrite,
		"metadata":             PermissionRead,
		"vulnerability_alerts": PermissionRead,
	}
}

// Sandbox is the sandbox block: where sandboxed runs execute and what
// they may reach (DESIGN-0005).
type Sandbox struct {
	// RuntimeClass is the sandbox's RuntimeClass (Kata).
	RuntimeClass string
	// ServiceAccount is the sandboxed Job's ServiceAccount, with no RBAC.
	ServiceAccount string
	// NodeSelector and Tolerations are the optional dedicated placement.
	NodeSelector map[string]string
	Tolerations  []corev1.Toleration
	// EgressAllow are the host names every sandboxed run may reach; a
	// profile's egress_allow adds to them.
	EgressAllow []string
	// FloorMode is FloorReport or FloorEnforce.
	FloorMode string
	// FloorAdd are managers sandboxed in addition to the built-in floor.
	FloorAdd []string
	// TokenPermissions is the sandboxed run's token, by GitHub permission.
	TokenPermissions map[string]string
	// PendingTimeout bounds how long a sandboxed pod may take to start.
	PendingTimeout time.Duration
}

// FloorMode is the configured floor mode: FloorReport without a sandbox
// block.
func (c *Config) FloorMode() string {
	if c.Sandbox == nil {
		return FloorReport
	}
	return c.Sandbox.FloorMode
}

func (b *builder) sandbox(s *sandboxBlock, runs *Runs) *Sandbox {
	if s == nil {
		return nil
	}
	out := &Sandbox{
		RuntimeClass:   s.RuntimeClass,
		ServiceAccount: derefOr(s.ServiceAccount, DefaultSandboxServiceAccount),
		NodeSelector:   maps.Clone(s.NodeSelector),
		Tolerations:    tolerations(s.Tolerations),
		EgressAllow:    b.stringList(s.EgressAllow),
		FloorMode:      b.enum(floorModeEnum, s.FloorMode, FloorReport),
		FloorAdd:       b.stringList(s.FloorAdd),
		PendingTimeout: b.duration(s.PendingTimeout, runs.PendingTimeout+SandboxBootAllowance),
	}
	out.TokenPermissions = maps.Clone(s.TokenPermissions)
	if out.TokenPermissions == nil {
		out.TokenPermissions = DefaultTokenPermissions()
	}
	return out
}

func tolerations(in []tolerationBlock) []corev1.Toleration {
	if len(in) == 0 {
		return nil
	}
	out := make([]corev1.Toleration, 0, len(in))
	for i := range in {
		t := &in[i]
		out = append(out, corev1.Toleration{
			Key:               deref(t.Key),
			Operator:          corev1.TolerationOperator(deref(t.Operator)),
			Value:             deref(t.Value),
			Effect:            corev1.TaintEffect(deref(t.Effect)),
			TolerationSeconds: t.TolerationSeconds,
		})
	}
	return out
}

// forbiddenTokenPermissions are never granted to a sandboxed run: they
// reach past the repository's content into its automation and settings.
var forbiddenTokenPermissions = []string{"administration", "secrets", "workflows"}

// validateSandbox checks the sandbox block and every profile's sandbox
// attributes (IMPL-0006 task 3.3).
func (b *builder) validateSandbox(cfg *Config, raw *fileSchema) {
	if s := raw.Sandbox; s != nil {
		b.validateSandboxBlock(s)
		if len(cfg.SandboxedProfiles()) == 0 {
			b.errorf(s.DefRange, "Sandbox without a sandboxed profile",
				"The floor raises repositories to the least strict sandboxed profile; set sandboxed = true on at least one profile.")
		}
	}
	for i := range raw.Profiles {
		rp := &raw.Profiles[i]
		p, ok := cfg.Profiles[rp.Name]
		if !ok {
			continue
		}
		b.validateProfileSandbox(cfg, rp, p)
	}
}

func (b *builder) validateSandboxBlock(s *sandboxBlock) {
	if errs := validation.IsDNS1123Subdomain(s.RuntimeClass); len(errs) > 0 {
		b.errorf(s.RuntimeClassRange, "Invalid runtime_class", "sandbox.runtime_class %q: %s.", s.RuntimeClass, strings.Join(errs, "; "))
	}
	if s.ServiceAccount != nil {
		if errs := validation.IsDNS1123Subdomain(*s.ServiceAccount); len(errs) > 0 {
			b.errorf(s.ServiceAccountRange, "Invalid service_account",
				"sandbox.service_account %q: %s.", *s.ServiceAccount, strings.Join(errs, "; "))
		}
	}
	b.validateHosts(s.EgressAllow, "sandbox.egress_allow")
	for _, e := range b.exprElems(s.FloorAdd) {
		if !unsafe.IsManager(e.name) {
			b.errorf(e.rng, "Unknown Renovate manager",
				"sandbox.floor_add: %q is not a manager of Renovate %s.", e.name, unsafe.RenovateVersion)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(s.TokenPermissions)) {
		switch v := s.TokenPermissions[k]; {
		case slices.Contains(forbiddenTokenPermissions, k):
			b.errorf(s.TokenPermissionsRange, "Forbidden token permission",
				"sandbox.token_permissions may not grant %q: a sandboxed run must not reach %s.", k, strings.Join(forbiddenTokenPermissions, ", "))
		case v != PermissionRead && v != PermissionWrite:
			b.errorf(s.TokenPermissionsRange, "Invalid token permission",
				"sandbox.token_permissions.%s is %q; use %q or %q.", k, v, PermissionRead, PermissionWrite)
		}
	}
}

func (b *builder) validateProfileSandbox(cfg *Config, rp *profileBlock, p *Profile) {
	where := "profile " + rp.Name
	for _, e := range b.exprElems(rp.UnsafeExecutions) {
		if !unsafe.IsGated(e.name) {
			b.errorf(e.rng, "Invalid unsafe_executions",
				"%s: %q is not an allowedUnsafeExecutions value; Renovate %s has %s.",
				where, e.name, unsafe.RenovateVersion, strings.Join(unsafe.Gated(), ", "))
		}
	}
	b.validateHosts(rp.EgressAllow, where+" egress_allow")
	if len(p.UnsafeExecutions) > 0 && !p.Sandboxed {
		b.errorf(rp.DefRange, "unsafe_executions on an ordinary profile",
			"%s grants unsafe_executions but is not sandboxed; set sandboxed = true or drop them.", where)
	}
	if !p.Sandboxed {
		return
	}
	if p.Pod.RuntimeClassName != nil || len(p.Pod.NodeSelector) > 0 || len(p.Pod.Tolerations) > 0 {
		b.errorf(rp.DefRange, "Placement on a sandboxed profile",
			"%s is sandboxed, so the sandbox block owns its runtime class and placement; "+
				"drop pod runtime_class_name, node_selector and toleration.", where)
	}
	if cfg.Sandbox == nil {
		summary := "Sandboxed profile without a sandbox"
		detail := where + " is sandboxed but the file has no sandbox block; its runs fail closed with NeedsSandbox."
		if cfg.FloorMode() == FloorEnforce {
			b.errorf(rp.DefRange, summary, "%s", detail)
		} else {
			b.warnf(rp.DefRange, summary, "%s", detail)
		}
	}
}

// validateHosts checks a list of egress host names, each a DNS name with
// an optional leading "*." wildcard.
func (b *builder) validateHosts(expr hcl.Expression, where string) {
	for _, e := range b.exprElems(expr) {
		if errs := validation.IsDNS1123Subdomain(strings.TrimPrefix(e.name, "*.")); len(errs) > 0 {
			b.errorf(e.rng, "Invalid host name", "%s: %q: %s.", where, e.name, strings.Join(errs, "; "))
		}
	}
}

type namedElem struct {
	name string
	rng  hcl.Range
}

// exprElems returns a list attribute's string elements with their
// ranges, quietly: build has already reported their shape.
func (b *builder) exprElems(expr hcl.Expression) []namedElem {
	if isNull(expr, b.ctx) {
		return nil
	}
	elems, diags := hcl.ExprList(expr)
	if diags.HasErrors() {
		return nil
	}
	out := make([]namedElem, 0, len(elems))
	for _, e := range elems {
		v, vd := e.Value(b.ctx)
		if vd.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			continue
		}
		out = append(out, namedElem{name: v.AsString(), rng: e.Range()})
	}
	return out
}
