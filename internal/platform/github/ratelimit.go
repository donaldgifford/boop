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

package github

import (
	"context"
	"errors"
	"net/http"
	"time"

	gogithub "github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/internal/workflows"
)

// ReadRateLimit reads GET /rate_limit and returns the resources the
// budget tracks, core and graphql (DESIGN-0001 § InstallationWorkflow).
// ObservedAt is the response's Date header, GitHub's clock rather than
// ours; it falls back to the local clock only when the header is absent
// or malformed. The endpoint does not count against the limit, so the
// call skips the client-side limiter.
func (c *Client) ReadRateLimit(ctx context.Context) (*workflows.Readings, error) {
	limits, resp, err := c.gh.RateLimit.Get(ctx)
	if err != nil {
		return nil, classifyErr(resp, err)
	}
	if limits.GetCore() == nil || limits.GetGraphQL() == nil {
		return nil, errors.New("github: /rate_limit lacks resources.core or resources.graphql")
	}
	return &workflows.Readings{
		Resources: map[string]workflows.Reading{
			workflows.ResourceCore:    toReading(limits.GetCore()),
			workflows.ResourceGraphQL: toReading(limits.GetGraphQL()),
		},
		ObservedAt: observedAt(resp),
	}, nil
}

func toReading(r *gogithub.Rate) workflows.Reading {
	return workflows.Reading{Limit: r.Limit, Remaining: r.Remaining, Reset: r.Reset.UTC()}
}

func observedAt(resp *gogithub.Response) time.Time {
	if resp != nil && resp.Response != nil {
		if t, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}
