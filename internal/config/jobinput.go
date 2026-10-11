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
	"maps"

	"github.com/donaldgifford/boop/internal/jobspec"
)

// BuildInput is the one constructor from the config file to the job
// builder (IMPL-0001 task 3.5): the namespace, image and dry-run mode,
// the jobspec.App from the renovate block and the app's secrets, and the
// named profile. The RunRenovate activity sets the per-run fields
// (Attempt, TokenSecret, WorkflowID, RunID, deadlines). Option maps are
// deep-copied, so concurrent runs never share nested values.
func (c *Config) BuildInput(app *App, profile string, repo jobspec.Repo) (*jobspec.BuildInput, error) {
	if app == nil {
		return nil, fmt.Errorf("config: no app for repository %d", repo.ID)
	}
	p, ok := c.Profiles[profile]
	if !ok {
		return nil, fmt.Errorf("config: unknown profile %q", profile)
	}
	return &jobspec.BuildInput{
		Namespace: c.Runs.Namespace,
		Image:     c.Renovate.Image,
		App: jobspec.App{
			Endpoint:     app.Endpoint,
			SharedPreset: c.Renovate.SharedPreset,
			Global:       cloneOptions(c.Renovate.Global),
			LogLevel:     c.Renovate.LogLevel,
			RedisURL:     c.Renovate.RedisURL.Reveal(),
			GitAuthor:    c.Renovate.GitAuthor,
		},
		Repo: repo,
		Profile: jobspec.Profile{
			Name:     p.Name,
			Pod:      clonePod(&p.Pod),
			Renovate: cloneOptions(p.Renovate),
		},
		DryRun: c.Renovate.DryRun,
	}, nil
}

// App returns the configured app named name, or nil.
func (c *Config) App(name string) *App {
	for _, a := range c.Apps {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// cloneOptions deep-copies JSON-shaped Renovate options.
func cloneOptions(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneOptions(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	default:
		return v
	}
}

func clonePod(p *jobspec.PodOverlay) jobspec.PodOverlay {
	out := jobspec.PodOverlay{
		Labels:                        maps.Clone(p.Labels),
		Annotations:                   maps.Clone(p.Annotations),
		NodeSelector:                  maps.Clone(p.NodeSelector),
		RuntimeClassName:              p.RuntimeClassName,
		TerminationGracePeriodSeconds: p.TerminationGracePeriodSeconds,
		WorkSizeLimit:                 p.WorkSizeLimit,
	}
	if p.Resources != nil {
		out.Resources = p.Resources.DeepCopy()
	}
	for i := range p.Tolerations {
		out.Tolerations = append(out.Tolerations, *p.Tolerations[i].DeepCopy())
	}
	return out
}
