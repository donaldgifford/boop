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
	"net/http"
	"strconv"
	"strings"

	"k8s.io/client-go/rest"
)

// RequestRecorder counts API server requests. Phase 6's metrics registry
// implements it as boopd_kube_requests_total{verb,resource,code}.
type RequestRecorder interface {
	RecordRequest(verb, resource, code string)
}

// Instrument returns a copy of cfg whose transport reports every request
// to rec. code is the HTTP status, or "error" when no response arrived.
func Instrument(cfg *rest.Config, rec RequestRecorder) *rest.Config {
	out := rest.CopyConfig(cfg)
	prev := out.WrapTransport
	out.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if prev != nil {
			rt = prev(rt)
		}
		return &instrumented{base: rt, rec: rec}
	}
	return out
}

type instrumented struct {
	base http.RoundTripper
	rec  RequestRecorder
}

func (t *instrumented) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	code := "error"
	if resp != nil {
		code = strconv.Itoa(resp.StatusCode)
	}
	verb, resource := classify(req)
	t.rec.RecordRequest(verb, resource, code)
	return resp, err
}

// classify maps a request to the Kubernetes verb and resource, with the
// subresource appended ("pods/log"). It reads the path shape only:
// /api/v1/... or /apis/<group>/<version>/..., namespaced or not.
func classify(req *http.Request) (verb, resource string) {
	segs := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	switch {
	case len(segs) >= 2 && segs[0] == "api":
		segs = segs[2:]
	case len(segs) >= 3 && segs[0] == "apis":
		segs = segs[3:]
	default:
		return strings.ToLower(req.Method), "other"
	}
	if len(segs) >= 3 && segs[0] == "namespaces" {
		segs = segs[2:]
	}
	if len(segs) == 0 {
		return strings.ToLower(req.Method), "other"
	}
	resource = segs[0]
	named := len(segs) >= 2
	if len(segs) >= 3 {
		resource += "/" + segs[2]
	}
	return verbOf(req, named), resource
}

func verbOf(req *http.Request, named bool) string {
	switch req.Method {
	case http.MethodGet:
		switch {
		case req.URL.Query().Get("watch") == "true":
			return "watch"
		case named:
			return "get"
		default:
			return "list"
		}
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		if named {
			return "delete"
		}
		return "deletecollection"
	default:
		return strings.ToLower(req.Method)
	}
}
