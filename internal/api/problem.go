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

// Copied from repo-guardian internal/api/problem.go at a72faf9, unchanged.

package api

import (
	"encoding/json"
	"net/http"

	"github.com/donaldgifford/boop/internal/api/gen"
)

const problemContentType = "application/problem+json"

// problem is an RFC 9457 problem of type typ, about:blank when empty.
func problem(r *http.Request, status int, typ, detail string) gen.Problem {
	if typ == "" {
		typ = "about:blank"
	}

	p := gen.Problem{Type: typ, Title: http.StatusText(status), Status: status}
	if detail != "" {
		p.Detail = &detail
	}

	if id := requestIDFrom(r.Context()); id != "" {
		p.RequestId = &id
	}

	return p
}

// writeProblem writes an RFC 9457 problem response of type about:blank.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	writeTypedProblem(w, r, status, "", detail)
}

// writeTypedProblem writes an RFC 9457 problem response of type typ.
func writeTypedProblem(w http.ResponseWriter, r *http.Request, status int, typ, detail string) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(problem(r, status, typ, detail)); err != nil {
		// The status is already sent; the client sees a short body.
		return
	}
}
