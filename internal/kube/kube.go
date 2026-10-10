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

// Package kube runs one Renovate Job through its lifecycle (DESIGN-0001
// § RunRenovate activity steps 3 to 11): create it suspended, create its
// token Secret, unsuspend it, wait for its pod to run, follow the pod's
// log, read the container's exit and delete the Job with foreground
// propagation. Deleting the Job is the only delete the package issues;
// the Secret and the pod go with it through owner references.
package kube

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
)

// JobNameLabel is the label the Job controller puts on a Job's pods.
const JobNameLabel = "batch.kubernetes.io/job-name"

// containerName is the Renovate container in jobspec's pod.
const containerName = "renovate"

// Defaults.
const (
	// DefaultDeleteTimeout bounds how long Delete waits for the Job, its
	// pod and its Secret to be gone.
	DefaultDeleteTimeout = 2 * time.Minute
	// DefaultPollInterval is how often ExitCode and Delete re-read state.
	DefaultPollInterval = time.Second
)

// Errors.
var (
	// ErrPending is returned by WaitRunning when no pod reached Running
	// in time (DESIGN-0001: the pending timeout).
	ErrPending = errors.New("kube: pod not running before the pending timeout")
	// ErrDeleteTimeout is returned when the Job is still present after
	// the delete timeout.
	ErrDeleteTimeout = errors.New("kube: job still present after the delete timeout")
	// ErrNoPod is returned when a Job has no pod to read.
	ErrNoPod = errors.New("kube: job has no pod")
)

// RestConfig returns the client config: the file in KUBECONFIG when set,
// otherwise the in-cluster service account.
func RestConfig() (*rest.Config, error) {
	if path := os.Getenv("KUBECONFIG"); path != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return nil, fmt.Errorf("kube: load %s: %w", path, err)
		}
		return cfg, nil
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("kube: in-cluster config: %w", err)
	}
	return cfg, nil
}

// Exit is how the Renovate container ended.
type Exit struct {
	// Code is the container's exit code; -1 when the container never
	// terminated on its own (DeadlineExceeded).
	Code int
	// Reason is the container's terminated reason ("Completed", "Error",
	// "OOMKilled") or the Job's failure reason ("DeadlineExceeded").
	Reason string
}

// Runner drives Jobs in one namespace. It is safe for concurrent use.
type Runner struct {
	cs            kubernetes.Interface
	namespace     string
	deleteTimeout time.Duration
	poll          time.Duration
	maxReconnects int
	backoff       time.Duration
}

// Option tunes a Runner.
type Option func(*Runner)

// WithDeleteTimeout overrides DefaultDeleteTimeout.
func WithDeleteTimeout(d time.Duration) Option { return func(r *Runner) { r.deleteTimeout = d } }

// WithPollInterval overrides DefaultPollInterval.
func WithPollInterval(d time.Duration) Option { return func(r *Runner) { r.poll = d } }

// NewRunner returns a Runner for namespace.
func NewRunner(cs kubernetes.Interface, namespace string, opts ...Option) *Runner {
	r := &Runner{
		cs:            cs,
		namespace:     namespace,
		deleteTimeout: DefaultDeleteTimeout,
		poll:          DefaultPollInterval,
		maxReconnects: DefaultMaxReconnects,
		backoff:       DefaultReconnectBackoff,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Namespace is the namespace the Runner creates Jobs in.
func (r *Runner) Namespace() string { return r.namespace }

// CreateSuspended creates job with spec.suspend forced to true, so its
// pod cannot start before CreateSecret and Unsuspend. The returned Job
// carries the UID the Secret's owner reference needs.
func (r *Runner) CreateSuspended(ctx context.Context, job *batchv1.Job) (*batchv1.Job, error) {
	job = job.DeepCopy()
	job.Namespace = r.namespace
	job.Spec.Suspend = ptr.To(true)
	out, err := r.cs.BatchV1().Jobs(r.namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube: create job %s: %w", job.Name, err)
	}
	return out, nil
}

// CreateSecret creates the run's token Secret.
func (r *Runner) CreateSecret(ctx context.Context, secret *corev1.Secret) (*corev1.Secret, error) {
	secret = secret.DeepCopy()
	secret.Namespace = r.namespace
	out, err := r.cs.CoreV1().Secrets(r.namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube: create secret %s: %w", secret.Name, err)
	}
	return out, nil
}

// Unsuspend lets the Job's pod start.
func (r *Runner) Unsuspend(ctx context.Context, name string) error {
	patch := []byte(`{"spec":{"suspend":false}}`)
	_, err := r.cs.BatchV1().Jobs(r.namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("kube: unsuspend job %s: %w", name, err)
	}
	return nil
}

// WaitRunning watches the Job's pod until it is Running, or has already
// finished, and returns it. If none gets there within timeout it returns
// ErrPending; the caller deletes the Job.
func (r *Runner) WaitRunning(ctx context.Context, name string, timeout time.Duration) (*corev1.Pod, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pods := r.cs.CoreV1().Pods(r.namespace)
	sel := metav1.ListOptions{LabelSelector: JobNameLabel + "=" + name}
	for {
		list, err := pods.List(ctx, sel)
		if err != nil {
			return nil, r.waitErr(ctx, name, err)
		}
		for i := range list.Items {
			if started(&list.Items[i]) {
				return &list.Items[i], nil
			}
		}
		opts := sel
		opts.ResourceVersion = list.ResourceVersion
		w, err := pods.Watch(ctx, opts)
		if err != nil {
			return nil, r.waitErr(ctx, name, err)
		}
		if pod := waitStarted(ctx, w); pod != nil {
			return pod, nil
		}
		if ctx.Err() != nil {
			return nil, r.waitErr(ctx, name, ctx.Err())
		}
		// The watch closed early; list again.
	}
}

// waitStarted returns the first started pod the watch reports, or nil
// when the watch closes or ctx is done.
func waitStarted(ctx context.Context, w watch.Interface) *corev1.Pod {
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.ResultChan():
			if !ok {
				return nil
			}
			pod, isPod := ev.Object.(*corev1.Pod)
			if isPod && ev.Type != watch.Deleted && started(pod) {
				return pod
			}
		}
	}
}

func (*Runner) waitErr(ctx context.Context, name string, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: job %s", ErrPending, name)
	}
	return fmt.Errorf("kube: wait for job %s: %w", name, err)
}

func started(p *corev1.Pod) bool {
	switch p.Status.Phase {
	case corev1.PodRunning, corev1.PodSucceeded, corev1.PodFailed:
		return true
	default:
		return false
	}
}

// Pod returns the Job's pod.
func (r *Runner) Pod(ctx context.Context, name string) (*corev1.Pod, error) {
	list, err := r.cs.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: JobNameLabel + "=" + name})
	if err != nil {
		return nil, fmt.Errorf("kube: list pods of job %s: %w", name, err)
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoPod, name)
	}
	return &list.Items[0], nil
}

// ExitCode waits until the Renovate container has terminated, or the Job
// failed with DeadlineExceeded, and reports how it ended.
func (r *Runner) ExitCode(ctx context.Context, name string) (Exit, error) {
	t := time.NewTicker(r.poll)
	defer t.Stop()
	for {
		if exit, done, err := r.readExit(ctx, name); err != nil || done {
			return exit, err
		}
		select {
		case <-ctx.Done():
			return Exit{}, fmt.Errorf("kube: exit of job %s: %w", name, ctx.Err())
		case <-t.C:
		}
	}
}

func (r *Runner) readExit(ctx context.Context, name string) (Exit, bool, error) {
	job, err := r.cs.BatchV1().Jobs(r.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Exit{}, false, fmt.Errorf("kube: get job %s: %w", name, err)
	}
	if deadlineExceeded(job) {
		return Exit{Code: -1, Reason: batchv1.JobReasonDeadlineExceeded}, true, nil
	}
	pod, err := r.Pod(ctx, name)
	if errors.Is(err, ErrNoPod) {
		return Exit{}, false, nil
	}
	if err != nil {
		return Exit{}, false, err
	}
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.Name == containerName && cs.State.Terminated != nil {
			// The deadline kill can land between the Job read above and
			// the pod read; read the Job again before trusting the exit.
			again, err := r.cs.BatchV1().Jobs(r.namespace).Get(ctx, name, metav1.GetOptions{})
			if err == nil && deadlineExceeded(again) {
				return Exit{Code: -1, Reason: batchv1.JobReasonDeadlineExceeded}, true, nil
			}
			return Exit{Code: int(cs.State.Terminated.ExitCode), Reason: cs.State.Terminated.Reason}, true, nil
		}
	}
	return Exit{}, false, nil
}

// deadlineExceeded reports whether the Job hit activeDeadlineSeconds. The
// controller sets FailureTarget before it kills the pod and Failed only
// after, so a container terminated by that kill (exit 143) must not be
// read as the run's own exit.
func deadlineExceeded(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobFailed || c.Type == batchv1.JobFailureTarget) &&
			c.Status == corev1.ConditionTrue && c.Reason == batchv1.JobReasonDeadlineExceeded {
			return true
		}
	}
	return false
}

// Delete deletes the Job with foreground propagation and waits, bounded,
// until it is gone; its pod and Secret are deleted first through their
// owner references. A Job that is already gone is not an error.
func (r *Runner) Delete(ctx context.Context, name string) error {
	jobs := r.cs.BatchV1().Jobs(r.namespace)
	err := jobs.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationForeground)})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("kube: delete job %s: %w", name, err)
	}
	ctx, cancel := context.WithTimeout(ctx, r.deleteTimeout)
	defer cancel()
	t := time.NewTicker(r.poll)
	defer t.Stop()
	for {
		_, err := jobs.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %s", ErrDeleteTimeout, name)
		case <-t.C:
		}
	}
}
