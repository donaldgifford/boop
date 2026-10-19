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

package workflows

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

const (
	testSettle      = 30 * time.Second
	testMinInterval = 5 * time.Minute
)

// newEventRW is a probe whose PlanRun returns the default ingest timings.
func newEventRW(t *testing.T, script ...runReply) *rwProbe {
	t.Helper()
	p := newRW(t, script...)
	p.settle, p.minInterval = testSettle, testMinInterval
	return p
}

func (p *rwProbe) signalAt(at time.Duration, name string, arg any) {
	p.env.RegisterDelayedCallback(func() { p.env.SignalWorkflow(name, arg) }, at)
}

// An event runs after the settle time, and a later event merges into the
// pending run, keeping its time and raising its priority.
func TestRepoEvent_SettlesAndMerges(t *testing.T) {
	t.Parallel()
	p := newEventRW(t, succeeded())
	in := state(t0)
	in.NextDue = t0.Add(testCadence)
	p.signalAt(time.Hour, EventSignal, Event{Reason: EventReasonPRClosed, Priority: PriorityNormal, DeliveryID: "d1"})
	p.signalAt(time.Hour+10*time.Second, EventSignal, Event{Reason: EventReasonDashboard, Priority: PriorityHigh, DeliveryID: "d2"})
	p.keepSeen(2 * time.Hour)
	if err := p.run(in, 2*time.Hour); err != nil && !isCanceled(err) {
		t.Fatalf("workflow: %v", err)
	}
	if want := []time.Time{t0.Add(time.Hour + testSettle)}; !slices.EqualFunc(p.runAt, want, time.Time.Equal) {
		t.Fatalf("runs at %v, want one at the first event + settle %v", p.runAt, want)
	}
	if prio := p.acquires[0].Priority; prio != PriorityHigh {
		t.Errorf("merged run acquired at priority %d, want the higher PriorityHigh", prio)
	}
	if s := p.query(); !s.EventDue.IsZero() || !s.NextDue.Equal(p.runAt[0].Add(testCadence)) {
		t.Errorf("state after the run: EventDue %v NextDue %v, want no event and a cadence from the run", s.EventDue, s.NextDue)
	}
}

// min_interval from the last run's start holds a normal event back; a
// person's checkbox click (high) skips it.
func TestRepoEvent_MinInterval(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		prio Priority
		want time.Duration // second run, from t0
	}{
		{"normal waits for min_interval", PriorityNormal, testMinInterval},
		{"high skips it", PriorityHigh, time.Minute + testSettle},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newEventRW(t, succeeded(), succeeded())
			in := state(t0) // due at t0: the first run starts then
			p.signalAt(time.Minute, EventSignal, Event{Reason: "x", Priority: tt.prio})
			p.keepSeen(time.Hour)
			if err := p.run(in, time.Hour); err != nil && !isCanceled(err) {
				t.Fatalf("workflow: %v", err)
			}
			want := []time.Time{t0, t0.Add(tt.want)}
			if !slices.EqualFunc(p.runAt, want, time.Time.Equal) {
				t.Errorf("runs at %v, want %v", p.runAt, want)
			}
		})
	}
}

// An event that arrives during a run gives exactly one more run, after
// the run, not none and not one per event.
func TestRepoEvent_DuringRunGivesOneMoreRun(t *testing.T) {
	t.Parallel()
	p := newEventRW(t, succeeded(), succeeded(), succeeded())
	p.grantAt = []time.Time{t0.Add(10 * time.Minute)} // the run waits in acquire until +10m
	in := state(t0)
	p.signalAt(5*time.Minute, EventSignal, Event{Reason: EventReasonPRClosed})
	p.signalAt(6*time.Minute, EventSignal, Event{Reason: EventReasonPRClosed})
	p.keepSeen(2 * time.Hour)
	if err := p.run(in, 2*time.Hour); err != nil && !isCanceled(err) {
		t.Fatalf("workflow: %v", err)
	}
	// The run starts at +10m; the buffered events then wait min_interval
	// from that start.
	want := []time.Time{t0.Add(10 * time.Minute), t0.Add(15 * time.Minute)}
	if !slices.EqualFunc(p.runAt, want, time.Time.Equal) {
		t.Errorf("runs at %v, want %v", p.runAt, want)
	}
}

// changed → gone ends the workflow, offboarded.
func TestRepoChanged_GoneEnds(t *testing.T) {
	t.Parallel()
	p := newEventRW(t)
	p.checks = []CheckRepoResult{{State: RepoGone}}
	in := state(t0)
	in.NextDue = t0.Add(testCadence)
	p.signalAt(time.Hour, ChangedSignal, Changed{Reason: ChangedReasonRemoved, DeliveryID: "d1"})
	if err := p.run(in, 2*time.Hour); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if !p.env.IsWorkflowCompleted() || len(p.runs) != 0 {
		t.Errorf("completed %v after %d runs, want completed with none", p.env.IsWorkflowCompleted(), len(p.runs))
	}
	if !slices.EqualFunc(p.checked, []time.Time{t0.Add(time.Hour)}, time.Time.Equal) {
		t.Errorf("checked at %v, want once at the signal", p.checked)
	}
}

// changed → present refreshes the state, re-plans, then runs as a
// normal event after the settle time.
func TestRepoChanged_PresentReplansThenRuns(t *testing.T) {
	t.Parallel()
	p := newEventRW(t, succeeded())
	p.checks = []CheckRepoResult{{State: RepoPresent, Slug: "acme/renamed", DefaultBranch: "trunk", Extends: []string{"config:python"}}}
	in := state(t0)
	in.NextDue = t0.Add(testCadence)
	p.signalAt(time.Hour, ChangedSignal, Changed{Reason: ChangedReasonConfig, InstallationID: 2})
	p.env.OnSignalExternalWorkflow(mock.Anything, InstallationWorkflowID(2), "", ReportSignal, mock.Anything).Return(nil).Maybe()
	p.keepSeen(2 * time.Hour)
	if err := p.run(in, 2*time.Hour); err != nil && !isCanceled(err) {
		t.Fatalf("workflow: %v", err)
	}
	if p.plans != 3 {
		t.Errorf("PlanRun ran %d times, want 3: start, re-plan after changed, the run", p.plans)
	}
	if !slices.EqualFunc(p.runAt, []time.Time{t0.Add(time.Hour + testSettle)}, time.Time.Equal) {
		t.Fatalf("runs at %v, want one at the changed + settle", p.runAt)
	}
	if r := p.runs[0]; r.Slug != "acme/renamed" || r.DefaultBranch != "trunk" || r.InstallationID != 2 {
		t.Errorf("run input = %+v, want the refreshed slug, branch and installation", r)
	}
	if p.checkInputs[0].InstallationID != 2 {
		t.Errorf("CheckRepo for installation %d, want the payload's 2", p.checkInputs[0].InstallationID)
	}
	if s := p.query(); !slices.Equal(s.Extends, []string{"config:python"}) {
		t.Errorf("extends = %q, want the checked ones", s.Extends)
	}
}

// A workflow started by SignalWithStart(changed) runs CheckRepo before
// its first run, even with NextDue already past. A new one (no NextDue)
// skips the jitter: its first run is the changed's, after settle.
func TestRepoChanged_SignalWithStartChecksFirst(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		nextDue time.Time
		want    time.Time
	}{
		{"new workflow", time.Time{}, t0.Add(testSettle)},
		{"NextDue past", t0.Add(-time.Hour), t0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newEventRW(t, succeeded())
			in := state(t0)
			in.NextDue = tt.nextDue
			p.signalAt(0, ChangedSignal, Changed{Reason: ChangedReasonConfig, DeliveryID: "d1"})
			var before RepoState
			p.env.RegisterDelayedCallback(func() { before = p.query() }, 10*time.Second)
			p.keepSeen(2 * time.Hour)
			if err := p.run(in, 2*time.Hour); err != nil && !isCanceled(err) {
				t.Fatalf("workflow: %v", err)
			}
			if tt.nextDue.IsZero() && !before.NextDue.Equal(t0.Add(testCadence)) {
				t.Errorf("NextDue before the run = %v, want a cadence from the start, not the jitter", before.NextDue)
			}
			if len(p.checked) != 1 || len(p.runAt) != 1 || p.checked[0].After(p.runAt[0]) || p.plans < 2 {
				t.Fatalf("checked %v, ran %v after %d plans; want CheckRepo and a re-plan before the run", p.checked, p.runAt, p.plans)
			}
			if !p.runAt[0].Equal(tt.want) {
				t.Errorf("first run at %v, want %v", p.runAt[0], tt.want)
			}
		})
	}
}

// ContinueAsNew carries a pending event, applying buffered ones first.
func TestRepoEvent_ContinueAsNewCarriesIt(t *testing.T) {
	t.Parallel()
	p := newEventRW(t, succeeded())
	p.grantAt = []time.Time{t0.Add(10 * time.Minute)}
	in := state(t0)
	in.Iterations = MaxIterations - 1
	p.signalAt(5*time.Minute, EventSignal, Event{Reason: EventReasonDashboard, Priority: PriorityHigh})
	err := p.run(in, time.Hour)
	var can *workflow.ContinueAsNewError
	if !errors.As(err, &can) {
		t.Fatalf("workflow: %v, want ContinueAsNew", err)
	}
	var next RepoState
	if err := converter.GetDefaultDataConverter().FromPayloads(can.Input, &next); err != nil {
		t.Fatal(err)
	}
	if want := t0.Add(10*time.Minute + testSettle); !next.EventDue.Equal(want) ||
		next.EventPriority != PriorityHigh || next.EventReason != EventReasonDashboard {
		t.Errorf("carried event %v %d %q, want %v high dashboard", next.EventDue, next.EventPriority, next.EventReason, want)
	}
	if next.LastRun == nil || !next.LastRun.StartedAt.Equal(t0.Add(10*time.Minute)) {
		t.Errorf("carried LastRun %+v, want its start for min_interval", next.LastRun)
	}
}
