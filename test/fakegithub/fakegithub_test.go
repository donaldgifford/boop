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

package fakegithub_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/donaldgifford/boop/test/fakegithub"
)

// probeResp is the part of a probe answer the tests read.
type probeResp struct {
	Data *struct {
		Nodes []map[string]any `json:"nodes"`
	} `json:"data"`
	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

// postProbe sends a config probe for ids repositories and paths
// expressions, returning the status, the raw body and the decoded body.
func postProbe(t *testing.T, s *fakegithub.Server, ids, paths int) (int, []byte, probeResp) {
	t.Helper()
	vars := map[string]any{}
	nodeIDs := make([]string, ids)
	for i := range nodeIDs {
		nodeIDs[i] = fmt.Sprintf("R_%d", i)
	}
	vars["ids"] = nodeIDs
	for i := range paths {
		vars[fmt.Sprintf("e%d", i)] = fmt.Sprintf("HEAD:p%d.json", i)
	}
	body, err := json.Marshal(map[string]any{"query": "query", "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL+"/api/graphql", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/graphql: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out probeResp
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
	}
	return resp.StatusCode, raw, out
}

func TestProbe_LookupLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		limit       int
		ids, paths  int
		wantStatus  int
		wantTimeout bool
	}{
		{name: "no limit", limit: 0, ids: 100, paths: 10, wantStatus: http.StatusOK},
		{name: "over the limit", limit: 100, ids: 100, paths: 10, wantStatus: http.StatusBadGateway, wantTimeout: true},
		{name: "at the limit", limit: 100, ids: 10, paths: 10, wantStatus: http.StatusOK},
		{name: "one over", limit: 99, ids: 10, paths: 10, wantStatus: http.StatusBadGateway, wantTimeout: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := fakegithub.New(t)
			s.SetGraphQLLookupLimit(tt.limit)
			code, raw, out := postProbe(t, s, tt.ids, tt.paths)
			if code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", code, tt.wantStatus, raw)
			}
			if tt.wantTimeout {
				if len(raw) != 0 {
					t.Errorf("timeout body = %q, want empty", raw)
				}
			} else if out.Data == nil || len(out.Data.Nodes) != tt.ids {
				t.Errorf("answer = %+v, want %d nodes", out, tt.ids)
			}
			wantTimeouts := 0
			if tt.wantTimeout {
				wantTimeouts = 1
			}
			if got := s.ProbeTimeouts(); got != wantTimeouts {
				t.Errorf("ProbeTimeouts() = %d, want %d", got, wantTimeouts)
			}
		})
	}
}

func TestProbe_TimeoutShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		shape      fakegithub.TimeoutShape
		wantStatus int
		wantMsg    string
	}{
		{shape: fakegithub.TimeoutBadGateway, wantStatus: http.StatusBadGateway},
		{shape: fakegithub.TimeoutGatewayTimeout, wantStatus: http.StatusGatewayTimeout},
		{shape: fakegithub.TimeoutGraphQLError, wantStatus: http.StatusOK, wantMsg: fakegithub.GraphQLTimeoutMessage},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.wantStatus), func(t *testing.T) {
			t.Parallel()
			s := fakegithub.New(t)
			s.SetGraphQLLookupLimit(1)
			s.SetGraphQLTimeoutShape(tt.shape)
			code, raw, out := postProbe(t, s, 2, 1)
			if code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", code, tt.wantStatus)
			}
			if tt.wantMsg == "" {
				if len(raw) != 0 {
					t.Errorf("body = %q, want empty", raw)
				}
				return
			}
			if out.Data != nil || len(out.Errors) != 1 || out.Errors[0].Message != tt.wantMsg {
				t.Errorf("answer = %s, want no data and the timeout message", raw)
			}
		})
	}
}

func TestProbe_NodeIDCap(t *testing.T) {
	t.Parallel()
	s := fakegithub.New(t)
	code, raw, out := postProbe(t, s, fakegithub.MaxNodeIDs+1, 1)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if out.Data != nil || len(out.Errors) != 1 || out.Errors[0].Type != "ARGUMENT_LIMIT" {
		t.Errorf("answer = %s, want ARGUMENT_LIMIT and no data", raw)
	}
	if code, _, _ := postProbe(t, s, fakegithub.MaxNodeIDs, 1); code != http.StatusOK {
		t.Errorf("100 ids: status = %d, want 200", code)
	}
}

func TestProbe_Sizes(t *testing.T) {
	t.Parallel()
	s := fakegithub.New(t)
	s.SetGraphQLLookupLimit(10)
	for _, n := range []int{5, 20, 1} {
		postProbe(t, s, n, 1)
	}
	if got, want := s.ProbeSizes(), []int{5, 20, 1}; !slices.Equal(got, want) {
		t.Errorf("ProbeSizes() = %v, want %v", got, want)
	}
	if got := s.ProbeQueries(); got != 3 {
		t.Errorf("ProbeQueries() = %d, want 3 (timed-out probes count)", got)
	}
	if got := s.ProbeTimeouts(); got != 1 {
		t.Errorf("ProbeTimeouts() = %d, want 1", got)
	}
}

func TestSecondaryLimit(t *testing.T) {
	t.Parallel()
	s := fakegithub.New(t)
	s.SetSecondaryLimit(fakegithub.SecondaryProbe, 1, 7*time.Second)
	s.SetSecondaryLimit(fakegithub.SecondaryRepos, 1, 0)

	code, raw, _ := postProbe(t, s, 1, 1)
	if code != http.StatusForbidden {
		t.Fatalf("limited probe status = %d, want 403 (body %q)", code, raw)
	}
	var body struct {
		Message          string `json:"message"`
		DocumentationURL string `json:"documentation_url"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.DocumentationURL != fakegithub.SecondaryLimitDocURL || body.Message != fakegithub.SecondaryLimitMessage {
		t.Errorf("limited body = %s, want GitHub's secondary-limit body", raw)
	}
	if code, _, _ := postProbe(t, s, 1, 1); code != http.StatusOK {
		t.Errorf("second probe status = %d, want 200 (one limited request)", code)
	}
	if got := len(s.ProbeTimes()); got != 2 {
		t.Errorf("ProbeTimes() has %d entries, want 2 (limited ones count)", got)
	}

	if code, h := get(t, s.URL+"/api/v3/installation/repositories"); code != http.StatusForbidden || h.Get("Retry-After") != "" {
		t.Errorf("limited listing = %d, Retry-After %q; want 403 without Retry-After", code, h.Get("Retry-After"))
	}
	if code, _ := get(t, s.URL+"/api/v3/installation/repositories"); code != http.StatusOK {
		t.Errorf("second listing = %d, want 200", code)
	}
}

func TestSecondaryLimit_RetryAfterHeader(t *testing.T) {
	t.Parallel()
	s := fakegithub.New(t)
	s.SetSecondaryLimit(fakegithub.SecondaryInstallations, 1, 7*time.Second)
	if code, h := get(t, s.URL+"/api/v3/app/installations"); code != http.StatusForbidden || h.Get("Retry-After") != "7" {
		t.Errorf("limited installations = %d, Retry-After %q; want 403 and 7", code, h.Get("Retry-After"))
	}
}

// get sends a GET and returns the status and headers.
func get(t *testing.T, url string) (int, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header
}
