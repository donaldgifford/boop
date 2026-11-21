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

package api

import (
	"testing"
	"time"
)

func TestRechecks_DedupeWithinTheWindow(t *testing.T) {
	t.Parallel()
	var d rechecks
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	if _, ok := d.recent(1, t0); ok {
		t.Fatal("recent before any recheck")
	}
	d.sent(1, t0, "req-1")
	if id, ok := d.recent(1, t0.Add(59*time.Second)); !ok || id != "req-1" {
		t.Errorf("recent at 59s = %q, %v; want req-1", id, ok)
	}
	if _, ok := d.recent(2, t0.Add(time.Second)); ok {
		t.Error("another repository is deduplicated")
	}
	if _, ok := d.recent(1, t0.Add(recheckWindow)); ok {
		t.Error("recent after the window")
	}
}

func TestPurlFilter(t *testing.T) {
	t.Parallel()
	exact := "pkg:npm/lodash@4.17.21"
	if m := purlFilter(&exact); m.exact == nil || *m.exact != exact || m.prefix != nil {
		t.Errorf("versioned purl = %+v, want an exact match", m)
	}
	scoped := "pkg:npm/%40types/node_x"
	m := purlFilter(&scoped)
	if m.exact != nil || *m.base != scoped || *m.prefix != `pkg:npm/\%40types/node\_x@%` {
		t.Errorf("unversioned purl = base %v prefix %v, want escaped wildcards", *m.base, *m.prefix)
	}
	if m := purlFilter(nil); m != (purlMatch{}) {
		t.Errorf("nil purl = %+v", m)
	}
}

func TestP95(t *testing.T) {
	t.Parallel()
	ds := make([]time.Duration, 0, 100)
	for i := 100; i >= 1; i-- {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	if got := p95(ds); got != 95*time.Millisecond {
		t.Errorf("p95 = %v, want 95ms", got)
	}
	if got := p95([]time.Duration{time.Second}); got != time.Second {
		t.Errorf("p95 of one = %v", got)
	}
}
