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
	"bytes"
	"context"
	"net/http"

	"github.com/donaldgifford/boop/api"
	"github.com/donaldgifford/boop/internal/api/gen"
)

// server implements the generated strict interface.
type server struct {
	opts     Options
	rechecks rechecks
	estate   estateCache
}

var _ gen.StrictServerInterface = (*server)(nil)

// principal returns the request's principal; its absence is a wiring
// bug answered 500, never a wider scope.
func principal(ctx context.Context) (*Principal, error) {
	p, ok := principalFrom(ctx)
	if !ok {
		return nil, statusError(http.StatusInternalServerError, "")
	}
	return p, nil
}

// GetMe returns the caller, its groups, whether it is an operator and
// the visible owners the store knows.
func (s *server) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	owners, err := s.opts.Reader.Owners(ctx, p.Visible.Store())
	if err != nil {
		return nil, err
	}
	groups := p.Groups
	if groups == nil {
		groups = []string{}
	}
	if owners == nil {
		owners = []string{}
	}
	return gen.GetMe200JSONResponse{
		Subject: p.Subject, Name: p.Name, Groups: groups,
		AllOwners: p.Visible.All(), Owners: owners, Operator: p.Operator,
	}, nil
}

// GetOpenAPI serves the contract as written.
func (*server) GetOpenAPI(context.Context, gen.GetOpenAPIRequestObject) (gen.GetOpenAPIResponseObject, error) {
	return gen.GetOpenAPI200ApplicationyamlResponse{Body: bytes.NewReader(api.Spec), ContentLength: int64(len(api.Spec))}, nil
}
