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

package ingest

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-github/v62/github"
)

// ErrUnknownEvent is an event type go-github does not decode: recorded
// and ignored.
var ErrUnknownEvent = errors.New("ingest: unknown event type")

// Payload is a verified delivery decoded into go-github's typed event,
// with the fields every event shares read out for routing and the
// record.
type Payload struct {
	// Event is X-GitHub-Event; DeliveryID is X-GitHub-Delivery.
	Event      string
	DeliveryID string
	// Typed is go-github's event: *github.IssuesEvent, *github.PushEvent, ...
	Typed any

	Action         string
	SenderLogin    string
	SenderType     string
	InstallationID int64
	// Repo is the event's repository, if it has one.
	Repo *RepoRef
}

// RepoRef is the repository an event is about.
type RepoRef struct {
	ID            int64
	Slug          string
	DefaultBranch string
}

// The shared fields' getters, as go-github's event types have them.
type (
	withAction       interface{ GetAction() string }
	withSender       interface{ GetSender() *github.User }
	withInstallation interface {
		GetInstallation() *github.Installation
	}
	withRepo interface{ GetRepo() *github.Repository }
)

// Parse decodes a verified payload with go-github's ParseWebHook and
// reads the shared fields. An event type go-github does not know is
// ErrUnknownEvent; a payload that does not decode is an error.
func Parse(event, deliveryID string, payload []byte) (*Payload, error) {
	typed, err := github.ParseWebHook(event, payload)
	if err != nil {
		if strings.Contains(err.Error(), "unknown X-Github-Event") {
			return nil, fmt.Errorf("%w %q", ErrUnknownEvent, event)
		}
		return nil, fmt.Errorf("ingest: decode %s payload: %w", event, err)
	}
	p := &Payload{Event: event, DeliveryID: deliveryID, Typed: typed}
	if e, ok := typed.(withAction); ok {
		p.Action = e.GetAction()
	}
	if e, ok := typed.(withSender); ok {
		p.SenderLogin, p.SenderType = e.GetSender().GetLogin(), e.GetSender().GetType()
	}
	if e, ok := typed.(withInstallation); ok {
		p.InstallationID = e.GetInstallation().GetID()
	}
	switch e := typed.(type) {
	case *github.PushEvent:
		// A push carries its own repository shape.
		if r := e.GetRepo(); r != nil {
			p.Repo = &RepoRef{ID: r.GetID(), Slug: r.GetFullName(), DefaultBranch: r.GetDefaultBranch()}
		}
	case withRepo:
		if r := e.GetRepo(); r != nil {
			p.Repo = &RepoRef{ID: r.GetID(), Slug: r.GetFullName(), DefaultBranch: r.GetDefaultBranch()}
		}
	}
	return p, nil
}

// subject is the record's subject: the issue or PR number, or a push's
// ref.
func (p *Payload) subject() string {
	switch e := p.Typed.(type) {
	case *github.IssuesEvent:
		return fmt.Sprintf("#%d", e.GetIssue().GetNumber())
	case *github.PullRequestEvent:
		return fmt.Sprintf("#%d", e.GetPullRequest().GetNumber())
	case *github.PushEvent:
		return e.GetRef()
	}
	return ""
}
