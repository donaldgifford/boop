package kube_test

import (
	"context"
	"errors"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	"github.com/donaldgifford/boop/internal/kube"
)

const ns = "boopd"

func job(name string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       batchv1.JobSpec{Suspend: ptr.To(false)},
	}
}

func pod(jobName string, phase corev1.PodPhase, terminated *corev1.ContainerStateTerminated) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: jobName + "-abcde", Namespace: ns, Labels: map[string]string{kube.JobNameLabel: jobName}},
		Status:     corev1.PodStatus{Phase: phase},
	}
	if terminated != nil {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "renovate", State: corev1.ContainerState{Terminated: terminated}}}
	}
	return p
}

func TestRunner_CreateSuspendedForcesSuspend(t *testing.T) {
	t.Parallel()
	cs := fake.NewClientset()
	r := kube.NewRunner(cs, ns)
	got, err := r.CreateSuspended(context.Background(), job("run-1-0"))
	if err != nil {
		t.Fatalf("CreateSuspended() = %v", err)
	}
	if got.Namespace != ns || got.Spec.Suspend == nil || !*got.Spec.Suspend {
		t.Errorf("CreateSuspended() job = ns %q suspend %v, want %q true", got.Namespace, got.Spec.Suspend, ns)
	}
	if err := r.Unsuspend(context.Background(), "run-1-0"); err != nil {
		t.Fatalf("Unsuspend() = %v", err)
	}
	after, err := cs.BatchV1().Jobs(ns).Get(context.Background(), "run-1-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *after.Spec.Suspend {
		t.Error("Unsuspend() left spec.suspend true")
	}
}

func TestRunner_WaitRunning(t *testing.T) {
	t.Parallel()
	t.Run("already running", func(t *testing.T) {
		t.Parallel()
		cs := fake.NewClientset(pod("run-1-0", corev1.PodRunning, nil))
		p, err := kube.NewRunner(cs, ns).WaitRunning(context.Background(), "run-1-0", time.Second)
		if err != nil || p.Status.Phase != corev1.PodRunning {
			t.Errorf("WaitRunning() = %v, %v; want the running pod", p, err)
		}
	})
	t.Run("becomes running", func(t *testing.T) {
		t.Parallel()
		cs := fake.NewClientset(pod("run-2-0", corev1.PodPending, nil))
		go func() {
			time.Sleep(100 * time.Millisecond)
			p := pod("run-2-0", corev1.PodRunning, nil)
			_, _ = cs.CoreV1().Pods(ns).UpdateStatus(context.Background(), p, metav1.UpdateOptions{})
		}()
		p, err := kube.NewRunner(cs, ns).WaitRunning(context.Background(), "run-2-0", 5*time.Second)
		if err != nil || p.Status.Phase != corev1.PodRunning {
			t.Errorf("WaitRunning() = %v, %v; want the running pod", p, err)
		}
	})
	t.Run("pending timeout", func(t *testing.T) {
		t.Parallel()
		cs := fake.NewClientset(pod("run-3-0", corev1.PodPending, nil))
		_, err := kube.NewRunner(cs, ns).WaitRunning(context.Background(), "run-3-0", 200*time.Millisecond)
		if !errors.Is(err, kube.ErrPending) {
			t.Errorf("WaitRunning() err = %v, want ErrPending", err)
		}
	})
}

func TestRunner_ExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		objects []runtime.Object
		want    kube.Exit
	}{
		{
			name:    "exit 0",
			objects: []runtime.Object{job("j"), pod("j", corev1.PodSucceeded, &corev1.ContainerStateTerminated{ExitCode: 0, Reason: "Completed"})},
			want:    kube.Exit{Code: 0, Reason: "Completed"},
		},
		{
			name:    "exit 3",
			objects: []runtime.Object{job("j"), pod("j", corev1.PodFailed, &corev1.ContainerStateTerminated{ExitCode: 3, Reason: "Error"})},
			want:    kube.Exit{Code: 3, Reason: "Error"},
		},
		{
			name:    "oom",
			objects: []runtime.Object{job("j"), pod("j", corev1.PodFailed, &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"})},
			want:    kube.Exit{Code: 137, Reason: "OOMKilled"},
		},
		{
			name: "deadline exceeded",
			objects: []runtime.Object{func() *batchv1.Job {
				j := job("j")
				j.Namespace = ns
				j.Status.Conditions = []batchv1.JobCondition{
					{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: batchv1.JobReasonDeadlineExceeded},
				}
				return j
			}()},
			want: kube.Exit{Code: -1, Reason: "DeadlineExceeded"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, o := range tt.objects {
				if j, ok := o.(*batchv1.Job); ok {
					j.Namespace = ns
				}
			}
			cs := fake.NewClientset(tt.objects...)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, err := kube.NewRunner(cs, ns, kube.WithPollInterval(10*time.Millisecond)).ExitCode(ctx, "j")
			if err != nil || got != tt.want {
				t.Errorf("ExitCode() = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestRunner_DeleteForeground(t *testing.T) {
	t.Parallel()
	j := job("run-1-0")
	j.Namespace = ns
	cs := fake.NewClientset(j)
	var policy *metav1.DeletionPropagation
	cs.PrependReactor("delete", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		policy = a.(k8stesting.DeleteActionImpl).DeleteOptions.PropagationPolicy
		return false, nil, nil
	})
	r := kube.NewRunner(cs, ns, kube.WithPollInterval(10*time.Millisecond))
	if err := r.Delete(context.Background(), "run-1-0"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if policy == nil || *policy != metav1.DeletePropagationForeground {
		t.Errorf("Delete() propagation = %v, want Foreground", policy)
	}
	if err := r.Delete(context.Background(), "run-1-0"); err != nil {
		t.Errorf("Delete() of a gone job = %v, want nil", err)
	}
}
