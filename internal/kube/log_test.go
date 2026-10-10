package kube_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/donaldgifford/boop/internal/kube"
)

// logServer is a minimal API server: the Job's pod list, the pod (Running
// until the log has been served twice) and its log. The first log stream
// breaks mid-line; the second replays from sinceTime.
type logServer struct {
	mu        sync.Mutex
	lines     []string
	breakAt   int // lines served whole before the break on the first stream
	streams   int
	sinceSeen []string
}

func (s *logServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const podPath = "/api/v1/namespaces/boopd/pods"
	switch r.URL.Path {
	case podPath:
		writeJSON(w, &corev1.PodList{Items: []corev1.Pod{*s.pod()}})
	case podPath + "/p":
		writeJSON(w, s.pod())
	case podPath + "/p/log":
		s.serveLog(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *logServer) pod() *corev1.Pod {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &corev1.Pod{
		TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "boopd"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if s.streams >= 2 {
		p.Status.Phase = corev1.PodSucceeded
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: "renovate", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
		}}
	}
	return p
}

func (s *logServer) serveLog(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.streams++
	n := s.streams
	s.sinceSeen = append(s.sinceSeen, r.URL.Query().Get("sinceTime"))
	s.mu.Unlock()

	if q := r.URL.Query(); q.Get("follow") != "true" || q.Get("timestamps") != "true" || q.Get("container") != "renovate" {
		http.Error(w, "want follow, timestamps and container=renovate", http.StatusBadRequest)
		return
	}
	if n == 1 {
		for _, l := range s.lines[:s.breakAt] {
			_, _ = fmt.Fprintln(w, l)
		}
		// Cut the next line in half and end the stream.
		_, _ = fmt.Fprint(w, s.lines[s.breakAt][:len(s.lines[s.breakAt])/2])
		return
	}
	// The replay starts at the second of sinceTime, so it repeats lines
	// the follower has already delivered.
	since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("sinceTime"))
	for _, l := range s.lines {
		ts, _, _ := strings.Cut(l, " ")
		t, _ := time.Parse(time.RFC3339Nano, ts)
		if !t.Before(since) {
			_, _ = fmt.Fprintln(w, l)
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func TestFollowLog_ReconnectsAndDedupes(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	lines := make([]string, 0, 6)
	for i := range 6 {
		// Two lines per second, so the replay from a whole second repeats one.
		ts := base.Add(time.Duration(i) * 500 * time.Millisecond).Format(time.RFC3339Nano)
		lines = append(lines, fmt.Sprintf(`%s {"msg":"line %d"}`, ts, i))
	}
	fake := &logServer{lines: lines, breakAt: 3}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	r := kube.NewRunner(cs, "boopd", kube.WithReconnectBackoff(10*time.Millisecond))

	var got []string
	err = r.FollowLog(context.Background(), "run-1-0", time.Time{}, func(l kube.Line) error {
		got = append(got, l.Text)
		return nil
	})
	if err != nil {
		t.Fatalf("FollowLog() = %v", err)
	}
	want := make([]string, 0, len(lines))
	for i := range lines {
		want = append(want, fmt.Sprintf(`{"msg":"line %d"}`, i))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("FollowLog() lines =\n%s\nwant each exactly once, in order:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if fake.streams != 2 {
		t.Errorf("log opened %d times, want 2 (one reconnect)", fake.streams)
	}
	if fake.sinceSeen[0] != "" || fake.sinceSeen[1] == "" {
		t.Errorf("sinceTime per stream = %q, want none then the last line's", fake.sinceSeen)
	}
}

func TestFollowLog_GivesUp(t *testing.T) {
	t.Parallel()
	// A log endpoint that always fails, with a pod that never finishes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/boopd/pods":
			writeJSON(w, &corev1.PodList{Items: []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "p"}}}})
		case "/api/v1/namespaces/boopd/pods/p":
			writeJSON(w, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}})
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	r := kube.NewRunner(cs, "boopd", kube.WithReconnectBackoff(time.Millisecond), kube.WithMaxReconnects(2))
	err = r.FollowLog(context.Background(), "run-1-0", time.Time{}, func(kube.Line) error { return nil })
	if !errors.Is(err, kube.ErrLogBroken) {
		t.Errorf("FollowLog() = %v, want ErrLogBroken", err)
	}
}
