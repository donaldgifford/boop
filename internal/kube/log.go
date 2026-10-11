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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// Line is one log line with the kubelet's timestamp.
type Line struct {
	Time time.Time
	Text string
}

// Log follower defaults.
const (
	// DefaultMaxReconnects bounds consecutive reconnects that deliver no
	// new line.
	DefaultMaxReconnects = 5
	// DefaultReconnectBackoff is the pause before a reconnect.
	DefaultReconnectBackoff = time.Second
)

// ErrLogBroken is returned when the log stream kept breaking without
// delivering new lines.
var ErrLogBroken = errors.New("kube: log stream broken")

// WithMaxReconnects overrides DefaultMaxReconnects.
func WithMaxReconnects(n int) Option { return func(r *Runner) { r.maxReconnects = n } }

// WithReconnectBackoff overrides DefaultReconnectBackoff.
func WithReconnectBackoff(d time.Duration) Option { return func(r *Runner) { r.backoff = d } }

// FollowLog streams the Renovate container's log of the Job's pod with
// follow=true&timestamps=true from since (zero for the start), calling fn
// with each complete line and its timestamp, in order, exactly once.
//
// A stream that breaks, or ends while the container still runs, is
// reopened with sinceTime from the last delivered line; replayed lines
// at or before it are dropped, and so is a partial line cut off by the
// break. After DefaultMaxReconnects reopenings in a row that deliver
// nothing new it returns ErrLogBroken. It returns nil once the stream
// ends with the container terminated, and fn's error if fn fails.
func (r *Runner) FollowLog(ctx context.Context, name string, since time.Time, fn func(Line) error) error {
	pod, err := r.Pod(ctx, name)
	if err != nil {
		return err
	}
	pods := r.cs.CoreV1().Pods(r.namespace)
	last := time.Time{}
	failures := 0
	for {
		opts := &corev1.PodLogOptions{Container: containerName, Follow: true, Timestamps: true}
		if from := laterOf(since, last); !from.IsZero() {
			opts.SinceTime = &metav1.Time{Time: from}
		}
		delivered, streamErr, fnErr := r.readOnce(ctx, pods.GetLogs(pod.Name, opts), last, fn)
		if fnErr != nil {
			return fnErr
		}
		progressed := delivered.After(last)
		last = delivered
		if streamErr == nil && r.containerDone(ctx, pod.Name) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if progressed {
			failures = 0
		} else {
			failures++
		}
		if failures > r.maxReconnects {
			return fmt.Errorf("%w: pod %s after %d reconnects: %w", ErrLogBroken, pod.Name, r.maxReconnects, streamErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.backoff):
		}
	}
}

// readOnce opens the stream and reads it to its end. It returns the last
// delivered timestamp, the stream's error (nil for a clean end) and fn's
// error.
func (*Runner) readOnce(ctx context.Context, req *rest.Request, after time.Time, fn func(Line) error) (time.Time, error, error) {
	stream, err := req.Stream(ctx)
	if err != nil {
		return after, fmt.Errorf("kube: open log: %w", err), nil
	}
	defer stream.Close()
	var fnErr error
	last, err := readLines(stream, after, func(l Line) error {
		if err := fn(l); err != nil {
			fnErr = err
			return err
		}
		return nil
	})
	if fnErr != nil {
		return last, nil, fnErr
	}
	return last, err, nil
}

// containerDone reports whether the Renovate container has terminated.
// An error reading the pod counts as not done, so the caller reconnects.
func (r *Runner) containerDone(ctx context.Context, podName string) bool {
	p, err := r.cs.CoreV1().Pods(r.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	for i := range p.Status.ContainerStatuses {
		if cs := &p.Status.ContainerStatuses[i]; cs.Name == containerName && cs.State.Terminated != nil {
			return true
		}
	}
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// readLines reads timestamped lines from rd, skipping any at or before
// after, and returns the last timestamp delivered. A final line without
// its newline is a broken stream's partial line and is not delivered.
func readLines(rd io.Reader, after time.Time, fn func(Line) error) (time.Time, error) {
	br := bufio.NewReaderSize(rd, 64<<10)
	last := after
	for {
		raw, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return last, nil
			}
			return last, fmt.Errorf("kube: read log: %w", err)
		}
		line, ok := parseLine(strings.TrimSuffix(raw, "\n"))
		if !ok || !line.Time.After(last) {
			continue
		}
		if err := fn(line); err != nil {
			return last, err
		}
		last = line.Time
	}
}

// parseLine splits "<RFC3339Nano> <text>".
func parseLine(s string) (Line, bool) {
	ts, text, ok := strings.Cut(s, " ")
	if !ok {
		return Line{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return Line{}, false
	}
	return Line{Time: t, Text: text}, true
}
