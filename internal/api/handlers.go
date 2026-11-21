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
	opts Options
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

// errNotImplemented answers the operations Phase 10 fills in.
var errNotImplemented = typedError(http.StatusNotImplemented, "not_implemented", "not implemented yet")

// ListDashboardItems is not implemented yet.
func (*server) ListDashboardItems(context.Context, gen.ListDashboardItemsRequestObject) (gen.ListDashboardItemsResponseObject, error) {
	return nil, errNotImplemented
}

// ListDeliveries is not implemented yet.
func (*server) ListDeliveries(context.Context, gen.ListDeliveriesRequestObject) (gen.ListDeliveriesResponseObject, error) {
	return nil, errNotImplemented
}

// ListDependencies is not implemented yet.
func (*server) ListDependencies(context.Context, gen.ListDependenciesRequestObject) (gen.ListDependenciesResponseObject, error) {
	return nil, errNotImplemented
}

// GetEstate is not implemented yet.
func (*server) GetEstate(context.Context, gen.GetEstateRequestObject) (gen.GetEstateResponseObject, error) {
	return nil, errNotImplemented
}

// GetEstateSeries is not implemented yet.
func (*server) GetEstateSeries(context.Context, gen.GetEstateSeriesRequestObject) (gen.GetEstateSeriesResponseObject, error) {
	return nil, errNotImplemented
}

// ListEvents is not implemented yet.
func (*server) ListEvents(context.Context, gen.ListEventsRequestObject) (gen.ListEventsResponseObject, error) {
	return nil, errNotImplemented
}

// ListInstallations is not implemented yet.
func (*server) ListInstallations(context.Context, gen.ListInstallationsRequestObject) (gen.ListInstallationsResponseObject, error) {
	return nil, errNotImplemented
}

// ListPackages is not implemented yet.
func (*server) ListPackages(context.Context, gen.ListPackagesRequestObject) (gen.ListPackagesResponseObject, error) {
	return nil, errNotImplemented
}

// ListPullRequests is not implemented yet.
func (*server) ListPullRequests(context.Context, gen.ListPullRequestsRequestObject) (gen.ListPullRequestsResponseObject, error) {
	return nil, errNotImplemented
}

// GetPullRequest is not implemented yet.
func (*server) GetPullRequest(context.Context, gen.GetPullRequestRequestObject) (gen.GetPullRequestResponseObject, error) {
	return nil, errNotImplemented
}

// ListRepositories is not implemented yet.
func (*server) ListRepositories(context.Context, gen.ListRepositoriesRequestObject) (gen.ListRepositoriesResponseObject, error) {
	return nil, errNotImplemented
}

// GetRepository is not implemented yet.
func (*server) GetRepository(context.Context, gen.GetRepositoryRequestObject) (gen.GetRepositoryResponseObject, error) {
	return nil, errNotImplemented
}

// ListRepositoryDependencies is not implemented yet.
func (*server) ListRepositoryDependencies(
	context.Context,
	gen.ListRepositoryDependenciesRequestObject,
) (gen.ListRepositoryDependenciesResponseObject, error) {
	return nil, errNotImplemented
}

// ListRepositoryEvents is not implemented yet.
func (*server) ListRepositoryEvents(context.Context, gen.ListRepositoryEventsRequestObject) (gen.ListRepositoryEventsResponseObject, error) {
	return nil, errNotImplemented
}

// RecheckRepository is not implemented yet.
func (*server) RecheckRepository(context.Context, gen.RecheckRepositoryRequestObject) (gen.RecheckRepositoryResponseObject, error) {
	return nil, errNotImplemented
}

// ListRepositoryRuns is not implemented yet.
func (*server) ListRepositoryRuns(context.Context, gen.ListRepositoryRunsRequestObject) (gen.ListRepositoryRunsResponseObject, error) {
	return nil, errNotImplemented
}

// ListRuns is not implemented yet.
func (*server) ListRuns(context.Context, gen.ListRunsRequestObject) (gen.ListRunsResponseObject, error) {
	return nil, errNotImplemented
}

// GetStatus is not implemented yet.
func (*server) GetStatus(context.Context, gen.GetStatusRequestObject) (gen.GetStatusResponseObject, error) {
	return nil, errNotImplemented
}

// ListUpdates is not implemented yet.
func (*server) ListUpdates(context.Context, gen.ListUpdatesRequestObject) (gen.ListUpdatesResponseObject, error) {
	return nil, errNotImplemented
}
