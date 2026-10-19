package ingest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/url"
	"testing"

	"github.com/donaldgifford/boop/test/fakegithub"
)

func TestVerify(t *testing.T) {
	t.Parallel()
	app := testConfig().Apps[0]
	body := payload(t, "issues-edited-dashboard")
	form := []byte("payload=" + url.QueryEscape(string(body)))

	tests := []struct {
		name        string
		signature   string
		contentType string
		body        []byte
		wantErr     bool
	}{
		{name: "valid", signature: fakegithub.Sign(testSecret, body), contentType: "application/json", body: body},
		{name: "form-encoded", signature: fakegithub.Sign(testSecret, form), contentType: "application/x-www-form-urlencoded", body: form},
		{name: "wrong secret", signature: fakegithub.Sign("not it", body), contentType: "application/json", body: body, wantErr: true},
		{name: "missing header", contentType: "application/json", body: body, wantErr: true},
		{name: "malformed", signature: "sha256=zz", contentType: "application/json", body: body, wantErr: true},
		{name: "sha1 only", signature: sha1Sign(testSecret, body), contentType: "application/json", body: body, wantErr: true},
		{
			name: "body changed after signing", signature: fakegithub.Sign(testSecret, body), contentType: "application/json",
			body: append([]byte(" "), body...), wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := verify(app, tt.signature, tt.contentType, tt.body)
			if tt.wantErr {
				if !errors.Is(err, ErrSignature) {
					t.Fatalf("verify = %v, want ErrSignature", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if !bytes.Equal(got, body) {
				t.Errorf("verify returned %d bytes, want the %d-byte JSON payload", len(got), len(body))
			}
		})
	}
}

// sha1Sign is the legacy X-Hub-Signature value, which go-github's
// ValidatePayload would accept.
func sha1Sign(secret string, body []byte) string {
	m := hmac.New(sha1.New, []byte(secret))
	m.Write(body)
	return "sha1=" + hex.EncodeToString(m.Sum(nil))
}
