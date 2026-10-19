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

// Package ingest is boopd's webhook ingest (DESIGN-0003): it verifies
// the boop-bot App's deliveries, routes each one to Temporal signals on
// the workflow that owns the repository or installation, and records
// every delivery, including the ones it ignores. It talks only to
// Temporal; it never calls GitHub and never holds an App key.
package ingest

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/internal/config"
)

// GitHub's webhook headers.
const (
	HeaderEvent     = "X-GitHub-Event"
	HeaderDelivery  = "X-GitHub-Delivery"
	HeaderTargetID  = "X-GitHub-Hook-Installation-Target-ID"
	HeaderSignature = "X-Hub-Signature-256"
)

// Rejections.
var (
	// ErrUnknownApp is a delivery for no App with a webhook block (400).
	ErrUnknownApp = errors.New("ingest: delivery for no configured webhook app")
	// ErrSignature is a missing, malformed or wrong signature (401).
	ErrSignature = errors.New("ingest: bad signature")
)

// appFor picks the App a delivery is for by its
// X-GitHub-Hook-Installation-Target-ID, the App's id.
func appFor(cfg *config.Config, targetID string) (*config.App, error) {
	id, err := strconv.ParseInt(targetID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: target id %q", ErrUnknownApp, targetID)
	}
	for _, app := range cfg.WebhookApps() {
		if app.AppID == id {
			return app, nil
		}
	}
	return nil, fmt.Errorf("%w: app id %d", ErrUnknownApp, id)
}

// verify checks body against X-Hub-Signature-256 under app's webhook
// secret and returns the JSON payload (the payload field of a
// form-encoded body). Only the SHA-256 signature is accepted: go-github
// would fall back to SHA-1, so a delivery without a sha256= signature is
// rejected before it is asked. The compare is constant-time
// (hmac.Equal, in go-github).
func verify(app *config.App, signature, contentType string, body []byte) ([]byte, error) {
	if !strings.HasPrefix(signature, "sha256=") {
		return nil, fmt.Errorf("%w: no sha256 signature", ErrSignature)
	}
	payload, err := github.ValidatePayloadFromBody(contentType, bytes.NewReader(body), signature, app.Webhook.Secret.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSignature, err)
	}
	return payload, nil
}
