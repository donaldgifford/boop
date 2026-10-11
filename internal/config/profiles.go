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

	"github.com/hashicorp/hcl/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/donaldgifford/boop/internal/jobspec"
)

// profiles builds every profile and resolves inherit: a child's pod
// fields and Renovate options merge over its parent's, maps key by key.
func (b *builder) profiles(raws []profileBlock) map[string]*Profile {
	byName := make(map[string]*profileBlock, len(raws))
	for i := range raws {
		p := &raws[i]
		if first, dup := byName[p.Name]; dup {
			b.errorf(p.DefRange, "Duplicate profile", "Profile %q is already defined at %s.", p.Name, first.DefRange)
			continue
		}
		byName[p.Name] = p
	}

	out := make(map[string]*Profile, len(byName))
	var resolve func(name string, chain []string) *Profile
	resolve = func(name string, chain []string) *Profile {
		if p, done := out[name]; done {
			return p
		}
		raw, ok := byName[name]
		if !ok {
			return nil // the inherit reference validator reports it
		}
		own := &Profile{Name: name, Pod: b.pod(raw.Pod)}
		if raw.Renovate != nil {
			own.Renovate = b.options(raw.Renovate, "profile "+name+" renovate")
		}
		if raw.Inherit != nil {
			parentName := *raw.Inherit
			if slices.Contains(chain, parentName) || parentName == name {
				b.errorf(raw.InheritRange, "Profile inheritance cycle",
					"Profile %q inherits through %s back to itself.", name, strings.Join(append(chain, name, parentName), " -> "))
			} else if parent := resolve(parentName, append(chain, name)); parent != nil {
				own = &Profile{
					Name:     name,
					Pod:      mergePod(&parent.Pod, &own.Pod),
					Renovate: mergeOptions(parent.Renovate, own.Renovate),
				}
			}
		}
		out[name] = own
		return own
	}
	for i := range raws {
		resolve(raws[i].Name, nil)
	}
	return out
}

func (b *builder) pod(p *podBlock) jobspec.PodOverlay {
	if p == nil {
		return jobspec.PodOverlay{}
	}
	out := jobspec.PodOverlay{
		Labels:                        maps.Clone(p.Labels),
		Annotations:                   maps.Clone(p.Annotations),
		NodeSelector:                  maps.Clone(p.NodeSelector),
		RuntimeClassName:              p.RuntimeClassName,
		TerminationGracePeriodSeconds: p.TerminationGrace,
	}
	if p.WorkSizeLimit != nil {
		if q, err := resource.ParseQuantity(*p.WorkSizeLimit); err != nil {
			b.errorf(p.WorkSizeLimitRange, "Invalid quantity", "work_size_limit %q: %v.", *p.WorkSizeLimit, err)
		} else {
			out.WorkSizeLimit = &q
		}
	}
	if r := p.Resources; r != nil {
		out.Resources = &corev1.ResourceRequirements{
			Requests: b.resourceList(r.Requests, r.RequestsRange, "requests"),
			Limits:   b.resourceList(r.Limits, r.LimitsRange, "limits"),
		}
	}
	for i := range p.Tolerations {
		t := &p.Tolerations[i]
		out.Tolerations = append(out.Tolerations, corev1.Toleration{
			Key:               deref(t.Key),
			Operator:          corev1.TolerationOperator(deref(t.Operator)),
			Value:             deref(t.Value),
			Effect:            corev1.TaintEffect(deref(t.Effect)),
			TolerationSeconds: t.TolerationSeconds,
		})
	}
	return out
}

func (b *builder) resourceList(in map[string]string, rng hcl.Range, what string) corev1.ResourceList {
	if len(in) == 0 {
		return nil
	}
	out := make(corev1.ResourceList, len(in))
	for name, s := range in {
		q, err := resource.ParseQuantity(s)
		if err != nil {
			b.errorf(rng, "Invalid quantity", "resources.%s.%s %q: %v.", what, name, s, err)
			continue
		}
		out[corev1.ResourceName(name)] = q
	}
	return out
}

// mergePod lays child over parent. Maps and resource lists merge key by
// key; a set scalar or a non-empty toleration list replaces the parent's.
func mergePod(parent, child *jobspec.PodOverlay) jobspec.PodOverlay {
	out := jobspec.PodOverlay{
		Labels:                        mergeMap(parent.Labels, child.Labels),
		Annotations:                   mergeMap(parent.Annotations, child.Annotations),
		NodeSelector:                  mergeMap(parent.NodeSelector, child.NodeSelector),
		RuntimeClassName:              firstSet(child.RuntimeClassName, parent.RuntimeClassName),
		TerminationGracePeriodSeconds: firstSet(child.TerminationGracePeriodSeconds, parent.TerminationGracePeriodSeconds),
		WorkSizeLimit:                 firstSet(child.WorkSizeLimit, parent.WorkSizeLimit),
		Tolerations:                   parent.Tolerations,
	}
	if len(child.Tolerations) > 0 {
		out.Tolerations = child.Tolerations
	}
	if parent.Resources != nil || child.Resources != nil {
		var p, c corev1.ResourceRequirements
		if parent.Resources != nil {
			p = *parent.Resources
		}
		if child.Resources != nil {
			c = *child.Resources
		}
		out.Resources = &corev1.ResourceRequirements{
			Requests: mergeMap(p.Requests, c.Requests),
			Limits:   mergeMap(p.Limits, c.Limits),
		}
	}
	return out
}

// mergeOptions deep-merges Renovate options: nested objects merge, any
// other value in the child replaces the parent's.
func mergeOptions(parent, child map[string]any) map[string]any {
	if parent == nil && child == nil {
		return nil
	}
	out := make(map[string]any, len(parent)+len(child))
	for k, v := range parent {
		out[k] = v
	}
	for k, cv := range child {
		pm, pok := out[k].(map[string]any)
		cm, cok := cv.(map[string]any)
		if pok && cok {
			out[k] = mergeOptions(pm, cm)
			continue
		}
		out[k] = cv
	}
	return out
}

func mergeMap[K comparable, V any](parent, child map[K]V) map[K]V {
	if len(parent) == 0 && len(child) == 0 {
		return nil
	}
	out := make(map[K]V, len(parent)+len(child))
	maps.Copy(out, parent)
	maps.Copy(out, child)
	return out
}

func firstSet[T any](a, b *T) *T {
	if a != nil {
		return a
	}
	return b
}
