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
	"errors"
	"math"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"

	"github.com/donaldgifford/boop/internal/jobspec"
)

func happyJob(t *testing.T) *batchv1.Job {
	t.Helper()
	job, err := jobspec.BuildJob(baseInput())
	if err != nil {
		t.Fatalf("BuildJob err = %v", err)
	}
	return job
}

func TestBuildJob_NameLabelsAnnotations(t *testing.T) {
	t.Parallel()
	job := happyJob(t)

	if job.Name != "run-101-0" || job.Namespace != "boopd" {
		t.Errorf("name/namespace = %q/%q", job.Name, job.Namespace)
	}
	wantLabels := map[string]string{
		jobspec.LabelName:           "boopd",
		jobspec.LabelComponent:      "renovate-run",
		jobspec.LabelManagedBy:      "boopd",
		jobspec.LabelRepoID:         "101",
		jobspec.LabelInstallationID: "55",
		jobspec.LabelProfile:        "baseline",
	}
	for k, v := range wantLabels {
		if got := job.Labels[k]; got != v {
			t.Errorf("Job.Labels[%q] = %q, want %q", k, got, v)
		}
		if got := job.Spec.Template.Labels[k]; got != v {
			t.Errorf("Pod.Labels[%q] = %q, want %q", k, got, v)
		}
	}
	wantAnnotations := map[string]string{
		jobspec.AnnotationWorkflowID: "repo/github/101",
		jobspec.AnnotationRunID:      "0199c0de-0000-7000-8000-000000000000",
	}
	for k, v := range wantAnnotations {
		if got := job.Annotations[k]; got != v {
			t.Errorf("Job.Annotations[%q] = %q, want %q", k, got, v)
		}
		if got := job.Spec.Template.Annotations[k]; got != v {
			t.Errorf("Pod.Annotations[%q] = %q, want %q", k, got, v)
		}
	}
	if len(job.OwnerReferences) != 0 {
		t.Errorf("the Job owns its Secret, not the other way round: %+v", job.OwnerReferences)
	}
}

func TestBuildJob_SpecKnobs(t *testing.T) {
	t.Parallel()
	job := happyJob(t)

	if job.Spec.Suspend == nil || !*job.Spec.Suspend {
		t.Error("Job must be created suspended until the token Secret exists")
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Errorf("BackoffLimit = %v, want 0", job.Spec.BackoffLimit)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 55*60 {
		t.Errorf("ActiveDeadlineSeconds = %v, want 3300", job.Spec.ActiveDeadlineSeconds)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 3600 {
		t.Errorf("TTLSecondsAfterFinished = %v, want 3600", job.Spec.TTLSecondsAfterFinished)
	}
	if job.Spec.CompletionMode != nil || job.Spec.Parallelism != nil || job.Spec.Completions != nil {
		t.Error("one pod for one repository: no indexed completion, parallelism or completions")
	}
	if job.Spec.Selector != nil {
		t.Error("the Job controller owns the selector")
	}
}

func TestBuildJob_DeadlineOverrides(t *testing.T) {
	t.Parallel()
	in := baseInput()
	in.ActiveDeadline = 20 * time.Minute
	in.TTLAfterFinished = 10 * time.Minute
	job, err := jobspec.BuildJob(in)
	if err != nil {
		t.Fatalf("BuildJob err = %v", err)
	}
	if *job.Spec.ActiveDeadlineSeconds != 1200 || *job.Spec.TTLSecondsAfterFinished != 600 {
		t.Errorf("deadline/ttl = %d/%d, want 1200/600", *job.Spec.ActiveDeadlineSeconds, *job.Spec.TTLSecondsAfterFinished)
	}
}

// TestBuildJob_PodSecurityRestricted asserts the fields PodSecurity
// admission's "restricted" profile requires, plus the ones DESIGN-0001 adds
// so the pod holds nothing but its token.
func TestBuildJob_PodSecurityRestricted(t *testing.T) {
	t.Parallel()
	pod := happyJob(t).Spec.Template.Spec

	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want Never", pod.RestartPolicy)
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("AutomountServiceAccountToken must be false")
	}
	if pod.EnableServiceLinks == nil || *pod.EnableServiceLinks {
		t.Error("EnableServiceLinks must be false")
	}
	if pod.TerminationGracePeriodSeconds == nil || *pod.TerminationGracePeriodSeconds != 30 {
		t.Errorf("TerminationGracePeriodSeconds = %v, want 30", pod.TerminationGracePeriodSeconds)
	}
	if pod.ServiceAccountName != "" {
		t.Errorf("ServiceAccountName = %q, want empty", pod.ServiceAccountName)
	}

	sc := pod.SecurityContext
	if sc == nil {
		t.Fatal("Pod.SecurityContext must be set")
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("RunAsNonRoot must be true")
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != jobspec.RenovateUID {
		t.Errorf("RunAsUser = %v, want %d", sc.RunAsUser, jobspec.RenovateUID)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("SeccompProfile = %+v, want RuntimeDefault", sc.SeccompProfile)
	}

	if len(pod.Containers) != 1 {
		t.Fatalf("Containers = %d, want 1", len(pod.Containers))
	}
	c := pod.Containers[0]
	if c.Name != "renovate" || c.Image != testImage {
		t.Errorf("container = %q %q", c.Name, c.Image)
	}
	if len(c.Command) != 0 || len(c.Args) != 0 {
		t.Errorf("the image's entrypoint runs Renovate; got command %v args %v", c.Command, c.Args)
	}
	csc := c.SecurityContext
	if csc == nil {
		t.Fatal("Container.SecurityContext must be set")
	}
	if csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation {
		t.Error("AllowPrivilegeEscalation must be false")
	}
	if csc.ReadOnlyRootFilesystem == nil || !*csc.ReadOnlyRootFilesystem {
		t.Error("ReadOnlyRootFilesystem must be true")
	}
	if csc.Capabilities == nil || len(csc.Capabilities.Drop) != 1 || csc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("Capabilities = %+v, want drop ALL", csc.Capabilities)
	}
}

func TestBuildJob_Volumes(t *testing.T) {
	t.Parallel()
	pod := happyJob(t).Spec.Template.Spec

	if len(pod.Volumes) != 2 {
		t.Fatalf("Volumes = %+v, want work and tmp", pod.Volumes)
	}
	for _, v := range pod.Volumes {
		if v.EmptyDir == nil {
			t.Errorf("volume %q must be an emptyDir", v.Name)
		}
	}
	mounts := map[string]string{}
	for _, m := range pod.Containers[0].VolumeMounts {
		mounts[m.Name] = m.MountPath
	}
	if mounts["work"] != "/work" || mounts["tmp"] != "/tmp" {
		t.Errorf("mounts = %v", mounts)
	}
}

func TestBuildJob_ProfileOverlay(t *testing.T) {
	t.Parallel()
	in := baseInput()
	limit := resource.MustParse("10Gi")
	in.Profile = jobspec.Profile{
		Name: "python",
		Pod: jobspec.PodOverlay{
			Labels:      map[string]string{"boopd.dev/egress": "python", jobspec.LabelName: "evil"},
			Annotations: map[string]string{"example.com/note": "x"},
			Resources: &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("6Gi")},
			},
			RuntimeClassName:              ptr.To("gvisor"),
			NodeSelector:                  map[string]string{"boopd.dev/pool": "untrusted"},
			Tolerations:                   []corev1.Toleration{{Key: "boopd.dev/untrusted", Operator: corev1.TolerationOpExists}},
			TerminationGracePeriodSeconds: ptr.To[int64](10),
			WorkSizeLimit:                 &limit,
		},
	}
	job, err := jobspec.BuildJob(in)
	if err != nil {
		t.Fatalf("BuildJob err = %v", err)
	}
	pod := job.Spec.Template.Spec

	if job.Labels["boopd.dev/egress"] != "python" || job.Labels[jobspec.LabelProfile] != "python" {
		t.Errorf("labels = %v", job.Labels)
	}
	if job.Labels[jobspec.LabelName] != "boopd" {
		t.Errorf("a profile must not override reserved labels: %v", job.Labels)
	}
	if job.Annotations["example.com/note"] != "x" {
		t.Errorf("annotations = %v", job.Annotations)
	}
	if pod.RuntimeClassName == nil || *pod.RuntimeClassName != "gvisor" {
		t.Errorf("RuntimeClassName = %v", pod.RuntimeClassName)
	}
	if pod.NodeSelector["boopd.dev/pool"] != "untrusted" || len(pod.Tolerations) != 1 {
		t.Errorf("nodeSelector/tolerations = %v / %v", pod.NodeSelector, pod.Tolerations)
	}
	if *pod.TerminationGracePeriodSeconds != 10 {
		t.Errorf("TerminationGracePeriodSeconds = %d, want 10", *pod.TerminationGracePeriodSeconds)
	}
	if mem := pod.Containers[0].Resources.Limits[corev1.ResourceMemory]; mem.String() != "6Gi" {
		t.Errorf("memory limit = %s, want 6Gi", mem.String())
	}
	var work *corev1.Volume
	for i := range pod.Volumes {
		if pod.Volumes[i].Name == "work" {
			work = &pod.Volumes[i]
		}
	}
	if work == nil || work.EmptyDir.SizeLimit == nil || work.EmptyDir.SizeLimit.String() != "10Gi" {
		t.Errorf("work volume = %+v, want 10Gi size limit", work)
	}
	// The overlay never reaches the security context.
	if !*pod.SecurityContext.RunAsNonRoot || !*pod.Containers[0].SecurityContext.ReadOnlyRootFilesystem {
		t.Error("security context must be unchanged by the profile")
	}
}

func TestBuildJob_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*jobspec.BuildInput)
		wantErr error
	}{
		{"no namespace", func(in *jobspec.BuildInput) { in.Namespace = "" }, jobspec.ErrMissingNamespace},
		{"no image", func(in *jobspec.BuildInput) { in.Image = "" }, jobspec.ErrMissingImage},
		{"no repo id", func(in *jobspec.BuildInput) { in.Repo.ID = 0 }, jobspec.ErrMissingRepo},
		{"no slug", func(in *jobspec.BuildInput) { in.Repo.Slug = "" }, jobspec.ErrMissingRepo},
		{"no token secret", func(in *jobspec.BuildInput) { in.TokenSecret = "" }, jobspec.ErrMissingTokenSecret},
		{"negative attempt", func(in *jobspec.BuildInput) { in.Attempt = -1 }, jobspec.ErrInvalidAttempt},
		{"forbidden option", func(in *jobspec.BuildInput) { in.App.Global = map[string]any{"allowScripts": true} }, jobspec.ErrForbiddenOption},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := baseInput()
			tt.mutate(in)
			if _, err := jobspec.BuildJob(in); !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildTokenSecret(t *testing.T) {
	t.Parallel()
	job := happyJob(t)
	job.UID = types.UID("job-uid-1")

	secret, err := jobspec.BuildTokenSecret(job, "ghs_example")
	if err != nil {
		t.Fatalf("BuildTokenSecret err = %v", err)
	}
	if secret.Name != job.Name || secret.Namespace != job.Namespace {
		t.Errorf("name/namespace = %q/%q, want the Job's", secret.Name, secret.Namespace)
	}
	if secret.Type != corev1.SecretTypeOpaque || secret.StringData[jobspec.TokenKey] != "ghs_example" {
		t.Errorf("type/data = %q/%v", secret.Type, secret.StringData)
	}
	if len(secret.OwnerReferences) != 1 {
		t.Fatalf("OwnerReferences = %+v, want the Job", secret.OwnerReferences)
	}
	ref := secret.OwnerReferences[0]
	if ref.Kind != "Job" || ref.APIVersion != "batch/v1" || ref.Name != job.Name || ref.UID != job.UID {
		t.Errorf("owner = %+v", ref)
	}
	if ref.BlockOwnerDeletion == nil || !*ref.BlockOwnerDeletion || ref.Controller == nil || !*ref.Controller {
		t.Error("owner reference must set BlockOwnerDeletion and Controller")
	}
	if secret.Labels[jobspec.LabelRepoID] != "101" {
		t.Errorf("labels = %v, want the Job's", secret.Labels)
	}
	// The token env var in the Job points at this Secret.
	tokenRef := envEntry(job.Spec.Template.Spec.Containers[0].Env, "RENOVATE_TOKEN").ValueFrom.SecretKeyRef
	if tokenRef.Name != secret.Name || secret.StringData[tokenRef.Key] == "" {
		t.Errorf("RENOVATE_TOKEN ref %+v does not match the Secret", tokenRef)
	}
}

func TestBuildTokenSecret_RequiresCreatedJob(t *testing.T) {
	t.Parallel()
	if _, err := jobspec.BuildTokenSecret(happyJob(t), "x"); !errors.Is(err, jobspec.ErrJobNotCreated) {
		t.Errorf("err = %v, want ErrJobNotCreated", err)
	}
	if _, err := jobspec.BuildTokenSecret(nil, "x"); !errors.Is(err, jobspec.ErrJobNotCreated) {
		t.Errorf("err = %v, want ErrJobNotCreated", err)
	}
}

func TestJobName_IsAlwaysADNSLabel(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		repoID  int64
		attempt int
		want    string
	}{
		{101, 0, "run-101-0"},
		{math.MaxInt64, 99, "run-9223372036854775807-99"},
	} {
		got := jobspec.JobName(tt.repoID, tt.attempt)
		if got != tt.want {
			t.Errorf("JobName(%d, %d) = %q, want %q", tt.repoID, tt.attempt, got, tt.want)
		}
		if errs := validation.IsDNS1123Label(got); len(errs) > 0 {
			t.Errorf("JobName(%d, %d) = %q is not a DNS label: %v", tt.repoID, tt.attempt, got, errs)
		}
	}
}
