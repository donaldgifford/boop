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

package kube

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

// RuntimeClassExists reports whether the cluster has the RuntimeClass
// (IMPL-0006 OQ2): a sandboxed run without it fails closed before a
// token is minted. It needs get on runtimeclasses, cluster-wide.
func (r *Runner) RuntimeClassExists(ctx context.Context, name string) (bool, error) {
	_, err := r.cs.NodeV1().RuntimeClasses().Get(ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		return true, nil
	case apierrors.IsNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("kube: get runtimeclass %s: %w", name, err)
	}
}

// Event reasons that put a pending sandboxed pod's cause in its runtime
// or placement rather than in cluster load.
const (
	reasonFailedScheduling   = "FailedScheduling"
	reasonFailedCreateSandox = "FailedCreatePodSandBox"
)

// SandboxPendingCause explains why a sandboxed Job's pod is not Running,
// when the reason is the sandbox: no pod at all (the RuntimeClass
// admission refused it), an unschedulable pod (no node the RuntimeClass
// and placement allow), or a pod sandbox the runtime could not create.
// It returns "" when it finds no such cause, or cannot look. Events need
// list on events; without it only the pod's own conditions count.
func (r *Runner) SandboxPendingCause(ctx context.Context, name string) string {
	pod, err := r.Pod(ctx, name)
	if errors.Is(err, ErrNoPod) {
		return "the Job created no pod; the RuntimeClass may be missing or refused"
	}
	if err != nil {
		return ""
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
			return "unschedulable: " + c.Message
		}
	}
	events, err := r.cs.CoreV1().Events(r.namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"involvedObject.kind": "Pod", "involvedObject.name": pod.Name}).String(),
	})
	if err != nil {
		return ""
	}
	for i := range events.Items {
		switch e := &events.Items[i]; e.Reason {
		case reasonFailedScheduling, reasonFailedCreateSandox:
			return e.Reason + ": " + e.Message
		}
	}
	return ""
}
