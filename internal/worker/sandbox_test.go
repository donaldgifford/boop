package worker_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/worker"
)

type fakeClasses struct {
	mu    sync.Mutex
	calls int
	steps []error // nil: exists; errNotFound: absent; other: failure
}

var errNotFound = errors.New("not found")

func (f *fakeClasses) RuntimeClassExists(context.Context, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	step := f.steps[min(f.calls, len(f.steps)-1)]
	f.calls++
	switch {
	case step == nil:
		return true, nil
	case errors.Is(step, errNotFound):
		return false, nil
	default:
		return false, step
	}
}

// WatchSandbox records the RuntimeClass at once and on every tick; a
// failed check is not ready (IMPL-0006 task 9.2).
func TestWatchSandbox(t *testing.T) {
	t.Parallel()
	rc := &fakeClasses{steps: []error{nil, errNotFound, errors.New("forbidden"), nil}}
	ctx, cancel := context.WithCancel(context.Background())
	var (
		mu  sync.Mutex
		got []bool
	)
	set := func(class string, ready bool) {
		if class != "kata" {
			t.Errorf("class = %q", class)
		}
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ready)
		if len(got) == 4 {
			cancel()
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.WatchSandbox(ctx, rc, "kata", time.Millisecond, set, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WatchSandbox did not return after cancel")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []bool{true, false, false, true}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("readings = %v, want %v first", got, want)
		}
	}
}
