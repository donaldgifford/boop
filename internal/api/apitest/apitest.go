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

// Copied from repo-guardian internal/api/apitest at a72faf9, unchanged
// but for the import paths.

// Package apitest is the API's contract harness (IMPL-0005 9.7). New
// is the only way API tests reach a server, and every exchange is
// validated against api/openapi.yaml with kin-openapi: an undeclared
// status, a missing required field or a wrong type fails the test. A
// depguard rule keeps net/http/httptest out of internal/api's tests so
// the harness cannot be bypassed.
package apitest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/donaldgifford/boop/internal/api"
	"github.com/donaldgifford/boop/internal/api/gen"
)

// Client calls a test API server and validates every exchange.
type Client struct {
	tb     testing.TB
	srv    *httptest.Server
	router routers.Router
}

// New starts the API built from opts and returns a validating client.
func New(tb testing.TB, opts *api.Options) *Client {
	tb.Helper()

	h, err := api.New(opts)
	if err != nil {
		tb.Fatalf("apitest: build API: %v", err)
	}

	srv := httptest.NewServer(h)
	tb.Cleanup(srv.Close)

	spec, err := gen.GetSpec()
	if err != nil {
		tb.Fatalf("apitest: load spec: %v", err)
	}

	spec.Servers = openapi3.Servers{{URL: srv.URL + api.BaseURL}}

	router, err := legacy.NewRouter(spec)
	if err != nil {
		tb.Fatalf("apitest: router: %v", err)
	}

	return &Client{tb: tb, srv: srv, router: router}
}

// Response is a validated response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Get requests path (relative to the API base, such as "/me") with
// token as the bearer, "" for none, and validates the exchange.
func (c *Client) Get(path, token string) *Response {
	c.tb.Helper()

	return c.send(http.MethodGet, path, token)
}

// Post sends an empty-bodied POST to path, as Get does.
func (c *Client) Post(path, token string) *Response {
	c.tb.Helper()

	return c.send(http.MethodPost, path, token)
}

func (c *Client) send(method, path, token string) *Response {
	c.tb.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, c.srv.URL+api.BaseURL+path, http.NoBody)
	if err != nil {
		c.tb.Fatalf("apitest: request: %v", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return c.Do(req)
}

// Do sends req and validates the response against the operation it
// routes to. A request the spec has no operation for fails the test.
func (c *Client) Do(req *http.Request) *Response {
	c.tb.Helper()

	route, params, err := c.router.FindRoute(req)
	if err != nil {
		c.tb.Fatalf("apitest: %s %s is not in the spec: %v", req.Method, req.URL.Path, err)
	}

	resp, err := c.srv.Client().Do(req)
	if err != nil {
		c.tb.Fatalf("apitest: %s %s: %v", req.Method, req.URL.Path, err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.tb.Fatalf("apitest: read body: %v", err)
	}

	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request: req, PathParams: params, Route: route,
			Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
		},
		Status: resp.StatusCode,
		Header: resp.Header,
		Options: &openapi3filter.Options{
			IncludeResponseStatus: true,
			MultiError:            true,
		},
	}
	input.SetBodyBytes(body)

	if err := openapi3filter.ValidateResponse(req.Context(), input); err != nil {
		c.tb.Errorf("apitest: %s %s answered %d, which violates the contract:\n%v\nbody: %s",
			req.Method, req.URL.Path, resp.StatusCode, err, body)
	}

	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}
}
