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
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/boop/internal/api/gen"
	"github.com/donaldgifford/boop/internal/store"
)

// The status page (DESIGN-0004 § Status page): aggregate-only, computed
// every StatusInterval into a cache the handler serves. It never names
// an owner, repository, package or installation and carries no error
// text.
const (
	StatusInterval = 30 * time.Second
	statusTimeout  = 10 * time.Second
	// staleAfter marks ingest and discovery degraded when their last
	// activity is older.
	staleAfter = 24 * time.Hour
)

// Component states.
const (
	stateOperational = "operational"
	stateDegraded    = "degraded"
	stateDown        = "down"
	stateUnknown     = "unknown"
)

// Component names (the closed set TestStatus_PrivacyGuard holds).
const (
	componentAPI        = "api"
	componentPostgres   = "postgres"
	componentTemporal   = "temporal"
	componentWorkers    = "workers"
	componentIngest     = "ingest"
	componentDeliveries = "deliveries"
	componentDiscovery  = "discovery"
	componentBudget     = "github_budget"
)

// StatusTemporal is what the status page asks of Temporal. client.Client
// satisfies it.
type StatusTemporal interface {
	CheckHealth(ctx context.Context, req *client.CheckHealthRequest) (*client.CheckHealthResponse, error)
	DescribeTaskQueue(
		ctx context.Context, taskQueue string, taskQueueType enumspb.TaskQueueType,
	) (*workflowservice.DescribeTaskQueueResponse, error)
}

// StatusOptions configures a StatusPage.
type StatusOptions struct {
	Reader Reader
	// Temporal may be nil: temporal and workers then read unknown.
	Temporal  StatusTemporal
	TaskQueue string
	Version   string
	Logger    *slog.Logger
	Now       func() time.Time
}

// StatusPage computes the status and serves the last computation.
type StatusPage struct {
	o   StatusOptions
	cur atomic.Pointer[gen.Status]
}

// NewStatusPage returns a page that answers unknown until its first
// Refresh.
func NewStatusPage(o *StatusOptions) *StatusPage {
	p := &StatusPage{o: *o}
	if p.o.Now == nil {
		p.o.Now = time.Now
	}
	if p.o.Logger == nil {
		p.o.Logger = slog.New(slog.DiscardHandler)
	}
	return p
}

// Current is the last computed status, or unknown before the first.
func (p *StatusPage) Current() gen.Status {
	if s := p.cur.Load(); s != nil {
		return *s
	}
	return gen.Status{
		State: stateUnknown, UpdatedAt: p.o.Now().UTC(), Version: p.o.Version,
		Components: []gen.StatusComponent{}, Fleet: gen.StatusFleet{},
	}
}

// Run refreshes every StatusInterval until ctx ends.
func (p *StatusPage) Run(ctx context.Context) {
	t := time.NewTicker(StatusInterval)
	defer t.Stop()
	for {
		p.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Refresh computes the status once.
func (p *StatusPage) Refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	now := p.o.Now().UTC()
	s := gen.Status{UpdatedAt: now, Version: p.o.Version}
	pg := p.postgres(ctx)
	s.Components = append(s.Components, gen.StatusComponent{Name: componentAPI, State: stateOperational}, pg)
	s.Components = append(s.Components, p.temporal(ctx)...)
	if pg.State != stateDown {
		if n, err := p.o.Reader.Status(ctx, now); err != nil {
			p.o.Logger.WarnContext(ctx, "status numbers failed", "error", err)
		} else {
			s.Fleet = gen.StatusFleet{Repositories: int(n.Repositories), OpenPrs: int(n.OpenPrs), Runs24h: int(n.Runs24h)}
			s.Components = append(s.Components, storeComponents(n, now)...)
		}
	}
	s.State = overall(s.Components)
	p.cur.Store(&s)
}

func (p *StatusPage) postgres(ctx context.Context) gen.StatusComponent {
	start := time.Now()
	if err := p.o.Reader.Ping(ctx); err != nil {
		p.o.Logger.WarnContext(ctx, "status: store ping failed", "error", err)
		return gen.StatusComponent{Name: componentPostgres, State: stateDown}
	}
	return gen.StatusComponent{Name: componentPostgres, State: stateOperational, LatencyMs: msSince(start)}
}

func (p *StatusPage) temporal(ctx context.Context) []gen.StatusComponent {
	if p.o.Temporal == nil {
		return []gen.StatusComponent{{Name: componentTemporal, State: stateUnknown}, {Name: componentWorkers, State: stateUnknown}}
	}
	t := gen.StatusComponent{Name: componentTemporal, State: stateOperational}
	start := time.Now()
	if _, err := p.o.Temporal.CheckHealth(ctx, &client.CheckHealthRequest{}); err != nil {
		p.o.Logger.WarnContext(ctx, "status: temporal health failed", "error", err)
		return []gen.StatusComponent{{Name: componentTemporal, State: stateDown}, {Name: componentWorkers, State: stateUnknown}}
	}
	t.LatencyMs = msSince(start)
	w := gen.StatusComponent{Name: componentWorkers, State: stateUnknown}
	resp, err := p.o.Temporal.DescribeTaskQueue(ctx, p.o.TaskQueue, enumspb.TASK_QUEUE_TYPE_WORKFLOW)
	if err != nil {
		p.o.Logger.WarnContext(ctx, "status: describe task queue failed", "error", err)
		return []gen.StatusComponent{t, w}
	}
	n := len(resp.GetPollers())
	w.Pollers, w.State = &n, stateOperational
	if n == 0 {
		w.State = stateDown
	}
	return []gen.StatusComponent{t, w}
}

// storeComponents are the components read from the store: ingest's and
// discovery's ages, the deliveries of the last 24 h, and the GitHub
// budget of each installation's latest run.
func storeComponents(n *store.StatusNumbers, now time.Time) []gen.StatusComponent {
	age := func(name string, last *time.Time) gen.StatusComponent {
		c := gen.StatusComponent{Name: name, State: stateUnknown}
		if last == nil {
			return c
		}
		a := int(now.Sub(*last).Seconds())
		c.AgeSeconds, c.State = &a, stateOperational
		if now.Sub(*last) > staleAfter {
			c.State = stateDegraded
		}
		return c
	}
	total, failed, missed := int(n.Deliveries24h), int(n.Failed24h), int(n.Missed24h)
	deliveries := gen.StatusComponent{Name: componentDeliveries, State: stateOperational, Total: &total, Failed: &failed, Missed: &missed}
	if failed+missed > 0 {
		deliveries.State = stateDegraded
	}
	installs, exhausted := 0, 0
	for _, sp := range n.Spends {
		var spend map[string]store.Spend
		if json.Unmarshal(sp.Spend, &spend) != nil {
			continue
		}
		installs++
		if c, ok := spend["core"]; ok && c.After == 0 {
			exhausted++
		}
	}
	budget := gen.StatusComponent{Name: componentBudget, State: stateUnknown}
	if installs > 0 {
		budget.Total, budget.Failed, budget.State = &installs, &exhausted, stateOperational
		if exhausted > 0 {
			budget.State = stateDegraded
		}
	}
	return []gen.StatusComponent{age(componentIngest, n.LastDelivery), deliveries, age(componentDiscovery, n.LastDiscovery), budget}
}

// overall is the worst known component state; unknown when none is
// known.
func overall(cs []gen.StatusComponent) string {
	rank := map[string]int{stateOperational: 1, stateDegraded: 2, stateDown: 3}
	worst := stateUnknown
	for _, c := range cs {
		if rank[c.State] > rank[worst] {
			worst = c.State
		}
	}
	return worst
}

func msSince(t time.Time) *int {
	ms := int(time.Since(t).Milliseconds())
	return &ms
}

// GetStatus serves the status page from its cache; it never queries.
func (s *server) GetStatus(context.Context, gen.GetStatusRequestObject) (gen.GetStatusResponseObject, error) {
	if s.opts.Status == nil {
		return gen.GetStatus200JSONResponse(NewStatusPage(&StatusOptions{Now: s.opts.Now}).Current()), nil
	}
	return gen.GetStatus200JSONResponse(s.opts.Status.Current()), nil
}
