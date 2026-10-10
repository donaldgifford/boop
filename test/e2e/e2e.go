//go:build e2e

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

// Package e2e is boopd's end-to-end suite (IMPL-0001 OQ3): it runs
// against a real API server and kubelet in a k3d cluster, with the stub
// Renovate image standing in for Renovate. `just e2e` creates the
// cluster, builds and imports the stub image and runs the suite with
// KUBECONFIG pointing at k3d; the "E2E Tests" CI job does the same.
//
// Each test gets its own namespace, labelled PodSecurity restricted the
// way the chart labels boopd's, and deleted when the test ends.
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/donaldgifford/boop/internal/kube"
)

// DefaultStubImage is the stub Renovate image bake builds and k3d imports.
const DefaultStubImage = "ghcr.io/donaldgifford/boopd-stub-renovate:dev"

// Env is what a test gets from Setup.
type Env struct {
	// Namespace is the test's own namespace.
	Namespace string
	// Clientset talks to the k3d cluster.
	Clientset kubernetes.Interface
	// Config is the rest config behind Clientset.
	Config *rest.Config
	// StubImage is the stub Renovate image reference.
	StubImage string
}

// Setup connects to the cluster in KUBECONFIG and creates a namespace for
// the test, deleted at cleanup.
func Setup(t *testing.T) *Env {
	t.Helper()
	if os.Getenv("KUBECONFIG") == "" {
		t.Fatal("KUBECONFIG is not set; run the suite with `just e2e`")
	}
	cfg, err := kube.RestConfig()
	if err != nil {
		t.Fatalf("rest config: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	image := os.Getenv("BOOPD_E2E_STUB_IMAGE")
	if image == "" {
		image = DefaultStubImage
	}

	name := "e2e-" + suffix(t)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: name,
		Labels: map[string]string{
			"pod-security.kubernetes.io/enforce": "restricted",
			"pod-security.kubernetes.io/warn":    "restricted",
			"boopd.dev/e2e":                      "true",
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := cs.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			t.Logf("delete namespace %s: %v", name, err)
		}
	})
	return &Env{Namespace: name, Clientset: cs, Config: cfg, StubImage: image}
}

func suffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
