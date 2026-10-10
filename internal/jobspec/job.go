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

// Package jobspec builds the Kubernetes Job that runs Renovate for one
// repository, and the environment that Job's container gets. Pure: it takes
// the configured App, the repository, the resolved profile and the name of
// the per-run token Secret, and returns objects ready for client.Create. No
// client calls, no clock, no mutation of inputs.
//
// Copied from renovate-operator internal/jobspec at 0183661 and adapted per
// DESIGN-0001 § Process builder and § Job spec: the CRD snapshots became
// plain inputs, the indexed Job and its shard ConfigMap became one pod for
// one repository, the entrypoint shell went away (RENOVATE_REPOSITORIES is
// set directly), and the pod gained the profile overlay, a suspended start,
// an owned token Secret and the remaining PodSecurity "restricted" fields.
//
// Validation here is about what the builder owns: options it sets through
// the environment are rejected in config, and so are Renovate's script and
// env controls. Whether a profile override is a globalOnly option is checked
// by the config package against Renovate's option list, not here.
package jobspec

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
)

// Label and annotation keys. Keep them lowercase per Kubernetes conventions.
const (
	LabelName      = "app.kubernetes.io/name"
	LabelComponent = "app.kubernetes.io/component"
	LabelManagedBy = "app.kubernetes.io/managed-by"

	LabelRepoID         = "boopd.dev/repo-id"
	LabelInstallationID = "boopd.dev/installation-id"
	LabelProfile        = "boopd.dev/profile"

	AnnotationWorkflowID = "boopd.dev/workflow-id"
	AnnotationRunID      = "boopd.dev/run-id"

	NameValue      = "boopd"
	ComponentValue = "renovate-run"

	// TokenKey is the data key in the per-run Secret that holds the
	// installation token. The container reads it as RENOVATE_TOKEN.
	TokenKey = "token"
)

// Defaults locked by DESIGN-0001 § Job spec and OQ10.
const (
	// DefaultActiveDeadline is the Job's activeDeadlineSeconds: a safety net
	// above the activity's soft deadline.
	DefaultActiveDeadline = 55 * time.Minute

	// DefaultTTLAfterFinished is ttlSecondsAfterFinished: a safety net for a
	// worker that died before deleting the Job.
	DefaultTTLAfterFinished = time.Hour

	// DefaultTerminationGracePeriod is how long the pod gets after SIGTERM.
	// Renovate installs no signal handler, so it exits at once anyway.
	DefaultTerminationGracePeriod = 30 * time.Second

	// RenovateUID is the uid of the Renovate image's "ubuntu" user.
	RenovateUID int64 = 12021

	containerName  = "renovate"
	workVolumeName = "work"
	tmpVolumeName  = "tmp"
	jobNamePrefix  = "run-"
)

// Build errors.
var (
	ErrMissingNamespace   = errors.New("jobspec: namespace is required")
	ErrMissingImage       = errors.New("jobspec: image is required")
	ErrMissingRepo        = errors.New("jobspec: repository id and slug are required")
	ErrMissingTokenSecret = errors.New("jobspec: token secret name is required")
	ErrInvalidAttempt     = errors.New("jobspec: attempt must be >= 0")
	ErrInvalidPreset      = errors.New("jobspec: shared preset must be a .json file")
	ErrForbiddenOption    = errors.New("jobspec: option is owned by boopd and cannot be set in config")
	ErrInvalidOption      = errors.New("jobspec: option has an invalid value")
	ErrJobNotCreated      = errors.New("jobspec: job must have a UID before its secret is built")
)

// App is the platform-level input: one configured GitHub App.
type App struct {
	// Endpoint is the API base URL. Empty or https://api.github.com means
	// github.com and leaves RENOVATE_ENDPOINT unset (renovate-operator
	// INV-0004 Observation 5).
	Endpoint string

	// SharedPreset is prepended to extends, e.g.
	// "github>boop-bot/renovate-config:default.json". A GitHub-hosted preset
	// must be a .json file.
	SharedPreset string

	// Global is the global Renovate config from the config file, merged into
	// RENOVATE_CONFIG. An explicit false survives the round trip.
	Global map[string]any

	// LogLevel is Renovate's LOG_LEVEL; "info" when empty.
	LogLevel string

	// RedisURL is the datasource cache, passed as RENOVATE_REDIS_URL when set.
	RedisURL string

	// GitAuthor is RENOVATE_GIT_AUTHOR when set.
	GitAuthor string
}

// Repo is the repository the Job runs against.
type Repo struct {
	ID             int64
	Slug           string
	DefaultBranch  string
	InstallationID int64
}

// Profile is the resolved ecosystem profile: a pod overlay and Renovate
// global overrides that are merged last into RENOVATE_CONFIG.
type Profile struct {
	Name     string
	Pod      PodOverlay
	Renovate map[string]any
}

// PodOverlay is what a profile may change on the pod. The security context
// is not part of it; every Job gets the restricted one.
type PodOverlay struct {
	Labels                        map[string]string
	Annotations                   map[string]string
	Resources                     *corev1.ResourceRequirements
	RuntimeClassName              *string
	NodeSelector                  map[string]string
	Tolerations                   []corev1.Toleration
	TerminationGracePeriodSeconds *int64
	WorkSizeLimit                 *resource.Quantity
}

// BuildInput is the closed set of inputs for the builder.
type BuildInput struct {
	Namespace string
	Image     string // the Renovate image, pinned by digest
	App       App
	Repo      Repo
	Profile   Profile
	Attempt   int

	// TokenSecret is the name of the per-run Secret the container reads
	// RENOVATE_TOKEN from. BuildTokenSecret names it after the Job.
	TokenSecret string

	// DryRun is "" or a Renovate dryRun mode such as "full". When set it
	// overrides any dryRun in the global config.
	DryRun string

	// WorkflowID and RunID are recorded as annotations for correlation.
	WorkflowID string
	RunID      string

	// ActiveDeadline and TTLAfterFinished override the defaults when > 0.
	ActiveDeadline   time.Duration
	TTLAfterFinished time.Duration
}

// JobName returns the Job name for a repository's run attempt. It is always
// a valid DNS-1123 label: "run-" plus at most 19 digits, a dash and the
// attempt number.
func JobName(repoID int64, attempt int) string {
	return jobNamePrefix + strconv.FormatInt(repoID, 10) + "-" + strconv.Itoa(attempt)
}

// Labels returns the label set for the Job, its pod and its Secret: the
// profile's labels first, then the reserved keys, which a profile cannot
// override.
func Labels(in *BuildInput) map[string]string {
	out := make(map[string]string, len(in.Profile.Pod.Labels)+6)
	for k, v := range in.Profile.Pod.Labels {
		out[k] = v
	}
	out[LabelName] = NameValue
	out[LabelComponent] = ComponentValue
	out[LabelManagedBy] = NameValue
	out[LabelRepoID] = strconv.FormatInt(in.Repo.ID, 10)
	out[LabelInstallationID] = strconv.FormatInt(in.Repo.InstallationID, 10)
	out[LabelProfile] = in.Profile.Name
	return out
}

// Annotations returns the annotation set for the Job and its pod: the
// profile's annotations plus the Temporal correlation keys when set.
func Annotations(in *BuildInput) map[string]string {
	out := make(map[string]string, len(in.Profile.Pod.Annotations)+2)
	for k, v := range in.Profile.Pod.Annotations {
		out[k] = v
	}
	if in.WorkflowID != "" {
		out[AnnotationWorkflowID] = in.WorkflowID
	}
	if in.RunID != "" {
		out[AnnotationRunID] = in.RunID
	}
	return out
}

// BuildJob materializes the Job for one run. It is created suspended so the
// caller can create the token Secret, owned by the Job, before unsuspending
// it. The returned Job has no UID; the caller is expected to client.Create
// it.
func BuildJob(in *BuildInput) (*batchv1.Job, error) {
	if err := validateJobInput(in); err != nil {
		return nil, err
	}
	env, err := BuildEnv(in)
	if err != nil {
		return nil, err
	}

	name := JobName(in.Repo.ID, in.Attempt)
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return nil, fmt.Errorf("jobspec: job name %q: %s", name, errs[0])
	}
	labels := Labels(in)
	annotations := Annotations(in)

	deadline := DefaultActiveDeadline
	if in.ActiveDeadline > 0 {
		deadline = in.ActiveDeadline
	}
	ttl := DefaultTTLAfterFinished
	if in.TTLAfterFinished > 0 {
		ttl = in.TTLAfterFinished
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   in.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: batchv1.JobSpec{
			Suspend:                 ptr.To(true),
			BackoffLimit:            ptr.To[int32](0),
			ActiveDeadlineSeconds:   ptr.To(int64(deadline.Seconds())),
			TTLSecondsAfterFinished: ptr.To(int32(ttl.Seconds())),
			Template:                podTemplate(in, env, labels, annotations),
		},
	}, nil
}

// BuildTokenSecret returns the per-run Secret for a created Job: same name,
// namespace and labels, owned by the Job so that deleting the Job deletes
// the token with it, holding the installation token under TokenKey.
func BuildTokenSecret(job *batchv1.Job, token string) (*corev1.Secret, error) {
	if job == nil || job.UID == "" {
		return nil, ErrJobNotCreated
	}
	labels := make(map[string]string, len(job.Labels))
	for k, v := range job.Labels {
		labels[k] = v
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Name,
			Namespace: job.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         batchv1.SchemeGroupVersion.String(),
				Kind:               "Job",
				Name:               job.Name,
				UID:                job.UID,
				Controller:         ptr.To(true),
				BlockOwnerDeletion: ptr.To(true),
			}},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{TokenKey: token},
	}, nil
}

func validateJobInput(in *BuildInput) error {
	switch {
	case in.Namespace == "":
		return ErrMissingNamespace
	case in.Image == "":
		return ErrMissingImage
	case in.Repo.ID <= 0 || in.Repo.Slug == "":
		return ErrMissingRepo
	case in.TokenSecret == "":
		return ErrMissingTokenSecret
	case in.Attempt < 0:
		return ErrInvalidAttempt
	}
	return nil
}

// podTemplate assembles the pod: restricted security context, no service
// account token, no service links, the profile overlay, and two emptyDirs.
func podTemplate(in *BuildInput, env []corev1.EnvVar, labels, annotations map[string]string) corev1.PodTemplateSpec {
	overlay := in.Profile.Pod

	grace := int64(DefaultTerminationGracePeriod.Seconds())
	if overlay.TerminationGracePeriodSeconds != nil {
		grace = *overlay.TerminationGracePeriodSeconds
	}
	work := corev1.EmptyDirVolumeSource{}
	if overlay.WorkSizeLimit != nil {
		work.SizeLimit = overlay.WorkSizeLimit
	}

	container := corev1.Container{
		Name:  containerName,
		Image: in.Image,
		Env:   env,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			ReadOnlyRootFilesystem:   ptr.To(true),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: workVolumeName, MountPath: workDir},
			{Name: tmpVolumeName, MountPath: tmpDir},
		},
	}
	if overlay.Resources != nil {
		container.Resources = *overlay.Resources
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: annotations},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			AutomountServiceAccountToken:  ptr.To(false),
			EnableServiceLinks:            ptr.To(false),
			TerminationGracePeriodSeconds: &grace,
			RuntimeClassName:              overlay.RuntimeClassName,
			NodeSelector:                  overlay.NodeSelector,
			Tolerations:                   overlay.Tolerations,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				RunAsUser:      ptr.To(RenovateUID),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{container},
			Volumes: []corev1.Volume{
				{Name: workVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &work}},
				{Name: tmpVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
		},
	}
}
