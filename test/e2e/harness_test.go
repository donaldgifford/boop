//go:build e2e

package e2e

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestHarness checks the harness itself: a namespace per test, labelled
// restricted, and the stub image named.
func TestHarness(t *testing.T) {
	env := Setup(t)
	ns, err := env.Clientset.CoreV1().Namespaces().Get(context.Background(), env.Namespace, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}
	if ns.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		t.Errorf("namespace labels = %v, want PodSecurity restricted", ns.Labels)
	}
	if env.StubImage == "" {
		t.Error("StubImage is empty")
	}
}
