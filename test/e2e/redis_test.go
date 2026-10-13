//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	"github.com/donaldgifford/boop/internal/workflows"
)

// redisPassword holds characters a URL must escape, so the run's URL
// only authenticates if the worker built it with the password escaped.
const redisPassword = "e2e p@ss/w:rd?#%"

// TestChart_RedisExistingSecret (IMPL-0002 task 3.10): with
// redis.auth.existingSecret naming a password-only Secret, redis starts
// with the ACL its init container wrote, the default user is off, and a
// run's RENOVATE_REDIS_URL, built by the worker from redis_url and the
// Secret, authenticates as renovate.
func TestChart_RedisExistingSecret(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	h := installChart(ctx, t, func(ctx context.Context, t *testing.T, h *chartHarness, values map[string]any) {
		t.Helper()
		h.secret(ctx, t, "e2e-redis", map[string][]byte{"pw": []byte(redisPassword)})
		values["redis"] = map[string]any{"auth": map[string]any{"existingSecret": "e2e-redis", "existingSecretKey": "pw"}}
	})
	cs, ns := h.env.Clientset, h.env.Namespace

	// Jobs are deleted when their run ends, so take the URL from the
	// first Job the watch sees.
	w, err := cs.BatchV1().Jobs(ns).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("watch jobs: %v", err)
	}
	defer w.Stop()
	urls := make(chan string, 1)
	go func() {
		for ev := range w.ResultChan() {
			if job, ok := ev.Object.(*batchv1.Job); ok {
				if u := jobEnv(job, "RENOVATE_REDIS_URL"); u != "" {
					urls <- u
					return
				}
			}
		}
	}()

	handle := h.srv.Client.ScheduleClient().GetHandle(ctx, workflows.DiscoveryScheduleID("e2e"))
	eventually(t, 2*time.Minute, "the discovery schedule", func() bool {
		_, err := handle.Describe(ctx)
		return err == nil
	})
	if err := handle.Trigger(ctx, client.ScheduleTriggerOptions{}); err != nil {
		t.Fatalf("trigger schedule: %v", err)
	}

	var url string
	select {
	case url = <-urls:
	case <-time.After(4 * time.Minute):
		t.Fatalf("no run Job with RENOVATE_REDIS_URL; worker log:\n%s", h.workerLogs(ctx, t))
	}
	if strings.Contains(url, redisPassword) {
		t.Errorf("RENOVATE_REDIS_URL carries the password unescaped")
	}

	host := "boopd-redis." + ns + ".svc"
	if out := h.redisCLI(ctx, t, "url", []string{"-u", "$(REDIS_URL)", "ping"}, url); !strings.Contains(out, "PONG") {
		t.Errorf("redis-cli -u <run URL> ping = %q, want PONG", out)
	}
	if out := h.redisCLI(ctx, t, "default", []string{"-h", host, "ping"}, ""); strings.Contains(out, "PONG") {
		t.Errorf("redis-cli as the default user = %q, want it refused", out)
	}
}

// jobEnv is the value of name in the Job's first container, or "".
func jobEnv(job *batchv1.Job, name string) string {
	for i := range job.Spec.Template.Spec.Containers {
		for _, e := range job.Spec.Template.Spec.Containers[i].Env {
			if e.Name == name {
				return e.Value
			}
		}
	}
	return ""
}

// redisCLI runs redis-cli with args in a one-off pod on the subchart's
// image and returns its output. url, if set, is REDIS_URL in the pod.
func (h *chartHarness) redisCLI(ctx context.Context, t *testing.T, name string, args []string, url string) string {
	t.Helper()
	cs, ns := h.env.Clientset, h.env.Namespace
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "redis-cli-" + name},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				RunAsUser:      ptr.To[int64](999),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:    "cli",
				Image:   redisImage(t),
				Command: append([]string{"redis-cli"}, args...),
				Env:     []corev1.EnvVar{{Name: "REDIS_URL", Value: url}},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
	if _, err := cs.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create %s: %v", pod.Name, err)
	}
	eventually(t, 2*time.Minute, pod.Name+" to finish", func() bool {
		p, err := cs.CoreV1().Pods(ns).Get(ctx, pod.Name, metav1.GetOptions{})
		return err == nil && (p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed)
	})
	raw, err := cs.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{}).DoRaw(ctx)
	if err != nil {
		t.Fatalf("%s log: %v", pod.Name, err)
	}
	return string(raw)
}

// redisImage is the subchart's pinned image, read from its values so the
// test never drifts from what the chart runs.
func redisImage(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "charts", "boopd", "charts", "redis", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Image struct {
			Repository string `json:"repository"`
			Tag        string `json:"tag"`
		} `json:"image"`
	}
	if err := yaml.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v.Image.Repository + ":" + v.Image.Tag
}
