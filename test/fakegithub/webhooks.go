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

package fakegithub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"
)

// Delivery is one webhook delivery the fake sent, as GitHub's
// /app/hook/deliveries reports it.
type Delivery struct {
	// ID is GitHub's per-attempt id; the list's cursor is one.
	ID int64
	// GUID is X-GitHub-Delivery, kept across redeliveries.
	GUID        string
	DeliveredAt time.Time
	Redelivery  bool
	// StatusCode is the receiver's answer; 0 when it never answered.
	StatusCode     int
	Event, Action  string
	InstallationID int64
	RepositoryID   int64
}

// deliverTimeout is how long GitHub waits for a webhook answer.
const deliverTimeout = 10 * time.Second

// DeliverOption adjusts one Deliver call.
type DeliverOption func(*deliverOptions)

type deliverOptions struct {
	guid   string
	secret *string
}

// WithGUID sends the delivery with a given X-GitHub-Delivery, as a
// redelivery does.
func WithGUID(guid string) DeliverOption { return func(o *deliverOptions) { o.guid = guid } }

// WithSecret signs with secret instead of the configured webhook secret.
func WithSecret(secret string) DeliverOption { return func(o *deliverOptions) { o.secret = &secret } }

// SetAppID sets the App ID sent as X-GitHub-Hook-Installation-Target-ID;
// the default is 1.
func (s *Server) SetAppID(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appID = id
}

// SetWebhookSecret sets the secret Deliver signs with.
func (s *Server) SetWebhookSecret(secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webhookSecret = secret
}

// Sign is GitHub's X-Hub-Signature-256 for body under secret.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Deliver POSTs payload to url as GitHub delivers event: JSON, signed
// with the webhook secret, with GitHub's headers. It records the answer
// for /app/hook/deliveries and returns the delivery; a connection
// failure is recorded with StatusCode 0.
func (s *Server) Deliver(tb testing.TB, url, event string, payload any, opts ...DeliverOption) Delivery {
	tb.Helper()
	o := deliverOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		tb.Fatalf("fakegithub: marshal %s payload: %v", event, err)
	}
	s.mu.Lock()
	secret, appID := s.webhookSecret, s.appID
	s.mu.Unlock()
	if o.secret != nil {
		secret = *o.secret
	}
	if o.guid == "" {
		o.guid = newGUID(tb)
	}

	ctx, cancel := context.WithTimeout(tb.Context(), deliverTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		tb.Fatalf("fakegithub: delivery request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GitHub-Hookshot/fake")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", o.guid)
	req.Header.Set("X-GitHub-Hook-Installation-Target-ID", strconv.FormatInt(appID, 10))
	req.Header.Set("X-GitHub-Hook-Installation-Target-Type", "integration")
	req.Header.Set("X-Hub-Signature-256", Sign(secret, body))

	status := 0
	if resp, err := http.DefaultClient.Do(req); err == nil {
		status = resp.StatusCode
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			tb.Logf("fakegithub: read the %s answer: %v", event, err)
		}
		_ = resp.Body.Close()
	}
	return s.record(event, body, o.guid, status)
}

// RecordUndelivered adds a delivery GitHub sent that never reached the
// receiver: no answer, so StatusCode 0.
func (s *Server) RecordUndelivered(tb testing.TB, event, action string) Delivery {
	tb.Helper()
	body, err := json.Marshal(map[string]any{"action": action})
	if err != nil {
		tb.Fatal(err)
	}
	return s.record(event, body, newGUID(tb), 0)
}

// Deliveries returns the recorded deliveries, oldest first.
func (s *Server) Deliveries() []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.deliveries)
}

// deliveryFields are the payload fields the deliveries list reports.
type deliveryFields struct {
	Action       string `json:"action"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Repository struct {
		ID int64 `json:"id"`
	} `json:"repository"`
}

// record appends one attempt; a GUID seen before is a redelivery.
func (s *Server) record(event string, body []byte, guid string, status int) Delivery {
	var p deliveryFields
	if err := json.Unmarshal(body, &p); err != nil {
		p = deliveryFields{} // a non-object payload records no fields
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := Delivery{
		ID: int64(len(s.deliveries) + 1), GUID: guid, DeliveredAt: time.Now().UTC().Truncate(time.Second),
		Redelivery: slices.ContainsFunc(s.deliveries, func(x Delivery) bool { return x.GUID == guid }),
		StatusCode: status, Event: event, Action: p.Action,
		InstallationID: p.Installation.ID, RepositoryID: p.Repository.ID,
	}
	s.deliveries = append(s.deliveries, d)
	return d
}

// DeleteInstallation deletes installation id: it leaves the App's
// listing and minting its token answers 404 from then on.
func (s *Server) DeleteInstallation(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted[id] = true
	s.installations = slices.DeleteFunc(s.installations, func(in Installation) bool { return in.ID == id })
}

// listDeliveries serves GET /app/hook/deliveries: newest first, per_page
// at a time, the next page linked by the last id as its cursor.
func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) {
	per := min(queryInt(r, "per_page", 30), 100)
	s.mu.Lock()
	defer s.mu.Unlock()
	all := slices.Clone(s.deliveries)
	slices.Reverse(all)
	if c := r.URL.Query().Get("cursor"); c != "" {
		at := slices.IndexFunc(all, func(d Delivery) bool { return strconv.FormatInt(d.ID, 10) == c })
		all = all[at+1:] // an unknown cursor (-1) starts from the newest
	}
	page := all[:min(per, len(all))]
	out := make([]map[string]any, 0, len(page))
	for i := range page {
		d := &page[i]
		status := "OK"
		if d.StatusCode == 0 {
			status = "Invalid HTTP Response: EOF"
		} else if d.StatusCode >= 300 {
			status = "Invalid HTTP Response: " + strconv.Itoa(d.StatusCode)
		}
		out = append(out, map[string]any{
			"id": d.ID, "guid": d.GUID, "delivered_at": d.DeliveredAt.Format(time.RFC3339),
			"redelivery": d.Redelivery, "duration": 0.1, "status": status, "status_code": d.StatusCode,
			"event": d.Event, "action": d.Action, "installation_id": d.InstallationID, "repository_id": d.RepositoryID,
		})
	}
	if len(all) > per {
		w.Header().Set("Link", fmt.Sprintf(`<%s/api/v3/app/hook/deliveries?per_page=%d&cursor=%d>; rel="next"`, s.URL, per, page[len(page)-1].ID))
	}
	writeJSON(w, http.StatusOK, out)
}

// newGUID is a random version-4 UUID, as X-GitHub-Delivery carries.
func newGUID(tb testing.TB) string {
	tb.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		tb.Fatal(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
