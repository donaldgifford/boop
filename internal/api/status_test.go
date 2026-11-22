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
	"errors"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/db"
)

// statusReader answers the status page's reads with names in every
// field it can, so a leak would show.
type statusReader struct {
	Reader

	pingErr error
}

func (r *statusReader) Ping(context.Context) error { return r.pingErr }

func (*statusReader) Status(_ context.Context, now time.Time) (*store.StatusNumbers, error) {
	last := now.Add(-time.Minute)
	old := now.Add(-48 * time.Hour)
	return &store.StatusNumbers{
		StatusNumbersRow: db.StatusNumbersRow{Repositories: 3, OpenPrs: 4, Runs24h: 5, Deliveries24h: 6, Failed24h: 1},
		LastDelivery:     &last, LastDiscovery: &old,
		Spends: []db.LatestSpendsRow{
			{InstallationID: 1, Spend: json.RawMessage(`{"core":{"before":5000,"after":0}}`)},
			{InstallationID: 2, Spend: json.RawMessage(`{"core":{"before":5000,"after":4000}}`)},
		},
	}, nil
}

type statusTemporal struct{ pollers int }

func (*statusTemporal) CheckHealth(context.Context, *client.CheckHealthRequest) (*client.CheckHealthResponse, error) {
	return &client.CheckHealthResponse{}, nil
}

func (t *statusTemporal) DescribeTaskQueue(
	context.Context, string, enumspb.TaskQueueType,
) (*workflowservice.DescribeTaskQueueResponse, error) {
	return &workflowservice.DescribeTaskQueueResponse{Pollers: make([]*taskqueuepb.PollerInfo, t.pollers)}, nil
}

// statusStrings are the only fields the status may carry text in; every
// other value is a number, a bool or null.
var statusStrings = map[string]bool{"state": true, "name": true, "version": true, "updated_at": true}

// statusNames are the component names the page may use.
var statusNames = map[string]bool{
	"api": true, "ingest": true, "workers": true, "temporal": true, "postgres": true,
	"discovery": true, "deliveries": true, "github_budget": true,
}

// TestStatus_PrivacyGuard: the status page carries no free text outside
// the allowlist, and its names come from a closed set.
func TestStatus_PrivacyGuard(t *testing.T) {
	t.Parallel()
	page := NewStatusPage(&StatusOptions{Reader: &statusReader{}, Temporal: &statusTemporal{pollers: 2}, Version: "v1.2.3"})
	page.Refresh(t.Context())
	b, err := json.Marshal(page.Current())
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				walk(k, e)
			}
		case []any:
			for _, e := range x {
				walk(key, e)
			}
		case string:
			if !statusStrings[key] {
				t.Errorf("status carries text in %q: %q", key, x)
			}
			if key == "name" && !statusNames[x] {
				t.Errorf("status names component %q", x)
			}
		}
	}
	walk("", v)
}

func TestStatus_States(t *testing.T) {
	t.Parallel()
	page := NewStatusPage(&StatusOptions{Reader: &statusReader{}, Temporal: &statusTemporal{pollers: 2}})
	page.Refresh(t.Context())
	got := map[string]string{}
	for _, c := range page.Current().Components {
		got[c.Name] = c.State
	}
	want := map[string]string{
		"api": stateOperational, "postgres": stateOperational, "temporal": stateOperational, "workers": stateOperational,
		"ingest": stateOperational, "deliveries": stateDegraded, "discovery": stateDegraded, "github_budget": stateDegraded,
	}
	for name, st := range want {
		if got[name] != st {
			t.Errorf("%s = %q, want %q", name, got[name], st)
		}
	}
	if s := page.Current(); s.State != stateDegraded || s.Fleet.Repositories != 3 {
		t.Errorf("status = %s with fleet %+v, want degraded with 3 repositories", s.State, s.Fleet)
	}

	down := NewStatusPage(&StatusOptions{Reader: &statusReader{pingErr: errors.New("refused")}, Temporal: &statusTemporal{}})
	down.Refresh(t.Context())
	if s := down.Current(); s.State != stateDown {
		t.Errorf("store down: status = %s, want down", s.State)
	}
	if s := NewStatusPage(&StatusOptions{Reader: &statusReader{}}).Current(); s.State != stateUnknown {
		t.Errorf("before the first refresh: status = %s, want unknown", s.State)
	}
}
