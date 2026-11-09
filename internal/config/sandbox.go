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
	"time"

	corev1 "k8s.io/api/core/v1"

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
