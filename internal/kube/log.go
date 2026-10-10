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
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Line is one log line with the kubelet's timestamp.
type Line struct {
	Time time.Time
	Text string
}

// FollowLog streams the Renovate container's log of the Job's pod with
// follow=true&timestamps=true from since (zero for the start), calling fn
// for each complete line in order. It returns when the stream ends, ctx
// is done or fn returns an error.
func (r *Runner) FollowLog(ctx context.Context, name string, since time.Time, fn func(Line) error) error {
	pod, err := r.Pod(ctx, name)
	if err != nil {
		return err
	}
	opts := &corev1.PodLogOptions{Container: containerName, Follow: true, Timestamps: true}
	if !since.IsZero() {
		opts.SinceTime = &metav1.Time{Time: since}
	}
	stream, err := r.cs.CoreV1().Pods(r.namespace).GetLogs(pod.Name, opts).Stream(ctx)
	if err != nil {
		return fmt.Errorf("kube: open log of %s: %w", pod.Name, err)
	}
	defer stream.Close()
	_, err = readLines(stream, time.Time{}, fn)
	return err
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
