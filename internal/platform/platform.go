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

// Package platform defines the platform-neutral types boopd's activities
// use: Repository, Installation, RepoState, the Client and Minter
// interfaces and the error sentinels. The GitHub implementation lives in
// internal/platform/github; boopd is GitHub-only (RFC-0001).
//
// Copied from renovate-operator internal/platform at 0183661 (INV-0001,
// Observation 7), with the Forgejo client dropped. Comments below that
// mention the Run reconciler, Scans or shards describe the operator's
// usage. The package moves to donaldgifford/x in Phase 0.
package platform

import (
	"context"
	"errors"
	"time"
)

// Repository is the discovery result for one repo. Only fields the reconciler
// reads are surfaced; richer metadata stays inside the platform-specific
// client.
type Repository struct {
	// ID is the platform's numeric repository id. It is stable across
	// renames and transfers, so it names the repository's workflow
	// (ADR-0002: repo/github/<id>).
	ID int64

	// NodeID is the platform's global node id, the handle the GraphQL
	// config probe passes to nodes(ids:) (DESIGN-0001 § DiscoveryWorkflow).
	NodeID string

	// Slug is the platform-qualified path ("owner/repo"). The same string
	// flows into RENOVATE_REPOSITORIES.
	Slug string

	// DefaultBranch is the repo's default branch (e.g., "main"). Used by
	// HasRenovateConfig to know which ref to query.
	DefaultBranch string

	// Archived is true for archived repos. Discovery may filter on this.
	Archived bool

	// Fork is true for forked repos. Discovery may filter on this.
	Fork bool

	// Topics are the repo's GitHub topics.
	Topics []string
}

// Installation is one installation of the GitHub App, as listed by
// GET /app/installations (DESIGN-0001 § DiscoveryWorkflow).
type Installation struct {
	// ID is the installation id; it names the InstallationWorkflow.
	ID int64

	// Account is the login of the user or organisation the App is
	// installed on.
	Account string

	// SuspendedAt is when the installation was suspended; zero when it
	// is active. Suspended installations are not discovered.
	SuspendedAt time.Time

	// RepositorySelection is "all" or "selected".
	RepositorySelection string
}

// Suspended reports whether the installation is suspended.
func (i *Installation) Suspended() bool { return !i.SuspendedAt.IsZero() }

// RepoState is CheckRepo's answer about one repository (DESIGN-0001
// § RepoWorkflow). The fourth answer, "could not tell", is an error, so
// an outage never looks like an offboarding.
type RepoState int

const (
	// RepoUnknown is the zero value; CheckRepo never returns it without
	// an error.
	RepoUnknown RepoState = iota
	// RepoGone means the installation can no longer see the repository,
	// or it is archived.
	RepoGone
	// RepoNoConfig means the repository is there but its default branch
	// lacks the config file.
	RepoNoConfig
	// RepoPresent means the repository is there with the config file.
	RepoPresent
)

// String implements fmt.Stringer.
func (s RepoState) String() string {
	switch s {
	case RepoGone:
		return "gone"
	case RepoNoConfig:
		return "no-config"
	case RepoPresent:
		return "present"
	default:
		return "unknown"
	}
}

// DiscoveryFilter is the platform-agnostic shape of a Scan's spec.discovery.
// The Run reconciler translates v1alpha1.DiscoverySpec into a DiscoveryFilter
// before calling Client.Discover.
type DiscoveryFilter struct {
	// Patterns are Renovate-style autodiscover globs ("owner/*", "owner/prefix-*").
	// Empty means no filter.
	Patterns []string

	// Topics restricts to repos with at least one matching topic.
	Topics []string

	// SkipForks drops fork repos.
	SkipForks bool

	// SkipArchived drops archived repos.
	SkipArchived bool

	// Owner is the org/user to enumerate against (e.g., "donaldgifford"). Set
	// once at Run start by the reconciler.
	Owner string
}

// Client is the small surface of platform-side work the reconciler needs.
// Implementations must be safe for concurrent use by multiple goroutines —
// HasRenovateConfig is dispatched concurrently by an errgroup during
// discovery.
type Client interface {
	// Discover enumerates the repos that survive filter/topics/skipForks/
	// skipArchived. The result is unsorted; the sharding builder sorts before
	// assigning shards.
	Discover(ctx context.Context, filter DiscoveryFilter) ([]Repository, error)

	// HasRenovateConfig returns true when the repo has the configured
	// Renovate config file (DefaultConfigPath unless overridden) on its
	// default branch.
	HasRenovateConfig(ctx context.Context, repo *Repository) (bool, error)

	// MintAccessToken returns a token that can authenticate to the platform's
	// git API. For GitHub App auth this is a freshly-minted installation
	// token (~1h TTL on github.com); for token auth it's the static
	// configured token returned unchanged with a zero expiresAt (PATs
	// don't expire on a fixed schedule).
	//
	// The Run reconciler calls this once per Run, writes the result into
	// the per-Run mirrored Secret as `access-token`, and the worker pod
	// consumes it as RENOVATE_TOKEN. See INV-0003.
	MintAccessToken(ctx context.Context) (token string, expiresAt time.Time, err error)
}

// Minter mints and revokes repository-scoped installation tokens for
// runs (DESIGN-0001 § RunRenovate activity steps 1 and 10, OQ2, OQ11).
// The spike's implementation holds the App key in memory; OpenBao
// Transit can sit behind the same seam later.
type Minter interface {
	// Mint returns an installation token restricted to repoIDs and its
	// expiry. Callers must not assume a token length.
	Mint(ctx context.Context, installationID int64, repoIDs []int64) (token string, expiresAt time.Time, err error)

	// Revoke ends token before its expiry.
	Revoke(ctx context.Context, token string) error
}

// DefaultConfigPath is the Renovate config file a repository must carry
// to be onboarded (ADR-0005). repo-guardian writes it; config may name a
// different single path, and every probe checks only that one path
// (DESIGN-0001 OQ3).
const DefaultConfigPath = "renovate.json"

// Error sentinels. Reconcilers distinguish transient (worth a retry) from
// permanent (set Ready=False with a clear reason and stop) so they can
// requeue intelligently.
var (
	// ErrTransient wraps any condition that's likely to clear on its own:
	// network blip, primary/secondary rate limit, 5xx. Reconcilers requeue.
	ErrTransient = errors.New("platform: transient error")

	// ErrPermanent wraps a condition that won't fix itself without user
	// intervention: 401/403 (bad credentials), 404 (org doesn't exist),
	// malformed config. Reconcilers surface via condition and stop.
	ErrPermanent = errors.New("platform: permanent error")

	// ErrUnauthorized is a refinement of ErrPermanent for 401/403 responses.
	// Reconcilers map it to Reason=AuthFailed.
	ErrUnauthorized = errors.New("platform: unauthorized")

	// ErrNotFound is a refinement of ErrPermanent for 404 responses on
	// known-good URL shapes (e.g., /orgs/{org}/repos for an org that doesn't exist).
	ErrNotFound = errors.New("platform: not found")
)

// RateLimitedError is returned when the upstream platform asked us to slow
// down. Carries the requested retry interval so the caller can wait the
// suggested duration before retrying.
type RateLimitedError struct {
	// RetryAfter is the duration the platform asked the caller to wait
	// before retrying. Zero means the platform did not suggest a value.
	RetryAfter time.Duration

	// Cause is the underlying error from the platform client (preserved for
	// logging / metrics).
	Cause error
}

// Error implements error. Always wraps ErrTransient so callers using
// errors.Is(err, platform.ErrTransient) match.
func (e *RateLimitedError) Error() string {
	if e.RetryAfter > 0 {
		return "platform: rate limited, retry after " + e.RetryAfter.String()
	}
	return "platform: rate limited"
}

// Unwrap reports ErrTransient so errors.Is(err, ErrTransient) returns true.
func (*RateLimitedError) Unwrap() error { return ErrTransient }
