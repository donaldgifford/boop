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

import "time"

// Metrics records the API's metrics (DESIGN-0004 § Metrics).
// observability.Metrics implements it.
type Metrics interface {
	// APIRequest is one request: boopd_api_requests_total and
	// boopd_api_request_seconds by route and code.
	APIRequest(route, code string, d time.Duration)
	// APIAuthFailure is one refused token: boopd_api_auth_failures_total
	// by reason.
	APIAuthFailure(reason string)
}

type nopMetrics struct{}

func (nopMetrics) APIRequest(string, string, time.Duration) {}
func (nopMetrics) APIAuthFailure(string)                    {}
