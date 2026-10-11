package kube_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"github.com/donaldgifford/boop/internal/kube"
)

type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) RecordRequest(verb, resource, code string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, verb+" "+resource+" "+code)
}

func TestInstrument(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/apis/batch/v1/namespaces/boopd/jobs/missing" {
			http.Error(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	rec := &recorder{}
	cs, err := kubernetes.NewForConfig(kube.Instrument(&rest.Config{Host: srv.URL}, rec))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = cs.BatchV1().Jobs("boopd").Get(ctx, "missing", metav1.GetOptions{})
	_, _ = cs.CoreV1().Pods("boopd").List(ctx, metav1.ListOptions{})
	_ = cs.BatchV1().Jobs("boopd").Delete(ctx, "run-1-0", metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationForeground)})
	_, _ = cs.CoreV1().Pods("boopd").GetLogs("p", &corev1.PodLogOptions{}).DoRaw(ctx)
	_, _ = cs.CoreV1().Namespaces().Get(ctx, "boopd", metav1.GetOptions{})

	want := []string{
		"get jobs 404",
		"list pods 200",
		"delete jobs 200",
		"get pods/log 200",
		"get namespaces 200",
	}
	if !slices.Equal(rec.seen, want) {
		t.Errorf("recorded = %q, want %q", rec.seen, want)
	}
}

func TestInstrument_TransportError(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	cs, err := kubernetes.NewForConfig(kube.Instrument(&rest.Config{Host: "http://127.0.0.1:1"}, rec))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = cs.CoreV1().Pods("boopd").Watch(context.Background(), metav1.ListOptions{})
	if len(rec.seen) == 0 || rec.seen[0] != "watch pods error" {
		t.Errorf("recorded = %q, want [watch pods error]", rec.seen)
	}
}
