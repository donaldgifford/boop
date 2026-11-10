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

package jobspec_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	"github.com/donaldgifford/boop/internal/jobspec"
)

var update = flag.Bool("update", false, "rewrite the golden Jobs in testdata")

func sandbox() *jobspec.Sandbox {
	return &jobspec.Sandbox{RuntimeClassName: "kata", ServiceAccountName: "boopd-sandbox"}
}

// sandboxedInput is a sandboxed jvm run granted the Gradle wrapper.
func sandboxedInput() *jobspec.BuildInput {
	in := baseInput()
	in.Profile = jobspec.Profile{Name: "jvm", Sandboxed: true, UnsafeExecutions: []string{"gradleWrapper"}}
	in.Sandbox = sandbox()
	return in
}

// TestBuildJob_Golden pins the Job for each shape IMPL-0006 Phase 5
// names (tasks 5.6, 5.7, 5.8). `go test ./internal/jobspec -update`
// rewrites the files.
func TestBuildJob_Golden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   func() *jobspec.BuildInput
	}{
		{name: "ordinary", in: baseInput},
		{name: "ordinary-enforce", in: func() *jobspec.BuildInput {
			in := baseInput()
			in.ForceDisable = []string{"bundler", "pip-compile", "poetry"}
			return in
		}},
		{name: "ordinary-host-users-false", in: func() *jobspec.BuildInput {
			in := baseInput()
			in.HostUsers = ptr.To(false)
			return in
		}},
		{name: "sandboxed", in: sandboxedInput},
		{name: "sandboxed-placement", in: func() *jobspec.BuildInput {
			in := sandboxedInput()
			in.Sandbox.NodeSelector = map[string]string{"boopd.dev/sandbox": "true"}
			in.Sandbox.Tolerations = []corev1.Toleration{
				{Key: "boopd.dev/sandbox", Value: "true", Effect: corev1.TaintEffectNoSchedule},
			}
			return in
		}},
		{name: "sandboxed-host-users-false", in: func() *jobspec.BuildInput {
			in := sandboxedInput()
			in.HostUsers = ptr.To(false) // never set on a sandboxed pod
			in.ForceDisable = []string{"poetry"}
			return in
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			job, err := jobspec.BuildJob(tt.in())
			if err != nil {
				t.Fatalf("BuildJob() = %v", err)
			}
			got, err := yaml.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", tt.name+".yaml")
			if *update {
				if err := os.WriteFile(path, got, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("Job differs from %s (run with -update after checking):\n%s", path, got)
			}
		})
	}
}

func TestBuildJob_SandboxedPod(t *testing.T) {
	t.Parallel()
	in := sandboxedInput()
	in.Profile.Pod.RuntimeClassName = ptr.To("runc") // the sandbox wins
	job, err := jobspec.BuildJob(in)
	if err != nil {
		t.Fatal(err)
	}
	pod := job.Spec.Template
	if got := pod.Spec.RuntimeClassName; got == nil || *got != "kata" {
		t.Errorf("runtimeClassName = %v, want kata", got)
	}
	if pod.Spec.ServiceAccountName != "boopd-sandbox" || *pod.Spec.AutomountServiceAccountToken {
		t.Errorf("serviceAccountName = %q, automount %v", pod.Spec.ServiceAccountName, *pod.Spec.AutomountServiceAccountToken)
	}
	if pod.Labels[jobspec.LabelSandbox] != "true" || job.Labels[jobspec.LabelSandbox] != "true" {
		t.Errorf("sandbox label missing: job %v, pod %v", job.Labels, pod.Labels)
	}
	if pod.Annotations[jobspec.AnnotationKataGuestSeccomp] != "false" {
		t.Errorf("pod annotations = %v, want guest seccomp on", pod.Annotations)
	}
	if pod.Spec.HostUsers != nil {
		t.Errorf("hostUsers = %v on a sandboxed pod", *pod.Spec.HostUsers)
	}
	res := pod.Spec.Containers[0].Resources
	if q := res.Requests[corev1.ResourceEphemeralStorage]; q.Cmp(jobspec.SandboxEphemeralRequest) != 0 {
		t.Errorf("ephemeral-storage request = %v", q.String())
	}
	if q := res.Limits[corev1.ResourceEphemeralStorage]; q.Cmp(jobspec.SandboxEphemeralLimit) != 0 {
		t.Errorf("ephemeral-storage limit = %v", q.String())
	}

	ordinary := happyJob(t)
	if _, ok := ordinary.Spec.Template.Annotations[jobspec.AnnotationKataGuestSeccomp]; ok {
		t.Error("an ordinary pod carries the Kata annotation")
	}
	if _, ok := ordinary.Labels[jobspec.LabelSandbox]; ok {
		t.Error("an ordinary Job carries the sandbox label")
	}
}

func renovateConfigOf(t *testing.T, in *jobspec.BuildInput) (cfg map[string]any, env map[string]string) {
	t.Helper()
	vars, err := jobspec.BuildEnv(in)
	if err != nil {
		t.Fatalf("BuildEnv() = %v", err)
	}
	env = make(map[string]string, len(vars))
	for _, v := range vars {
		env[v.Name] = v.Value
	}
	if err := json.Unmarshal([]byte(env[envConfig]), &cfg); err != nil {
		t.Fatalf("RENOVATE_CONFIG: %v", err)
	}
	return cfg, env
}

func TestBuildEnv_Sandboxed(t *testing.T) {
	t.Parallel()
	cfg, env := renovateConfigOf(t, sandboxedInput())
	if _, ok := env["RENOVATE_REDIS_URL"]; ok {
		t.Error("a sandboxed run gets RENOVATE_REDIS_URL")
	}
	if got := cfg["allowedUnsafeExecutions"]; !reflect.DeepEqual(got, []any{"gradleWrapper"}) {
		t.Errorf("allowedUnsafeExecutions = %v, want [gradleWrapper]", got)
	}
	if _, ok := cfg["force"]; ok {
		t.Error("a sandboxed run forces managers off")
	}
}

func TestBuildEnv_ForceDisable(t *testing.T) {
	t.Parallel()
	in := baseInput()
	in.ForceDisable = []string{"pip-compile", "poetry"}
	cfg, env := renovateConfigOf(t, in)
	if env["RENOVATE_REDIS_URL"] != testRedis {
		t.Error("an ordinary run lost its Redis URL")
	}
	force, _ := cfg["force"].(map[string]any)
	pipCompile, _ := force["pip-compile"].(map[string]any)
	if pipCompile["enabled"] != false || !reflect.DeepEqual(pipCompile["managerFilePatterns"], []any{}) {
		t.Errorf("force.pip-compile = %v, want disabled with []", pipCompile)
	}
	poetry, _ := force["poetry"].(map[string]any)
	if pats, _ := poetry["managerFilePatterns"].([]any); poetry["enabled"] != false || len(pats) == 0 {
		t.Errorf("force.poetry = %v, want disabled with its default patterns", poetry)
	}
	if _, ok := cfg["allowedUnsafeExecutions"]; ok {
		t.Error("an ordinary run gets allowedUnsafeExecutions")
	}

	in.ForceDisable = []string{"gradle"} // patterns built at run time
	if _, err := jobspec.BuildEnv(in); !errors.Is(err, jobspec.ErrNoDefaultPatterns) {
		t.Errorf("BuildEnv(force gradle) = %v, want ErrNoDefaultPatterns", err)
	}
}

func TestBuild_NeedsSandbox(t *testing.T) {
	t.Parallel()
	for name, p := range map[string]jobspec.Profile{
		"sandboxed":         {Name: "python", Sandboxed: true},
		"unsafe executions": {Name: "jvm", UnsafeExecutions: []string{"gradleWrapper"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := baseInput()
			in.Profile = p
			if _, err := jobspec.BuildJob(in); !errors.Is(err, jobspec.ErrNeedsSandbox) {
				t.Errorf("BuildJob() = %v, want ErrNeedsSandbox", err)
			}
		})
	}
}
