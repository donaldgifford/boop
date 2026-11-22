//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// apiClient reaches the API pod and the mock issuer through
// port-forwards, and signs in as either user.
type apiClient struct {
	base   string // the API's /api/v1 through the port-forward
	issuer string // the issuer through its port-forward
	http   *http.Client
	tokens map[string]string
}

func (h *storeHarness) apiClient(ctx context.Context, t *testing.T) *apiClient {
	t.Helper()
	apiPort := forward(ctx, t, h.env, "app.kubernetes.io/name=boopd-api,app.kubernetes.io/instance=boopd", 8080)
	issuerPort := forward(ctx, t, h.env, "app=mock-oidc", 9443)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(h.caPEM) {
		t.Fatal("e2e CA does not parse")
	}
	// The issuer's certificate names mock-oidc, not 127.0.0.1.
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "mock-oidc", MinVersion: tls.VersionTLS12}}
	return &apiClient{
		base:   fmt.Sprintf("http://127.0.0.1:%d/api/v1", apiPort),
		issuer: fmt.Sprintf("https://127.0.0.1:%d", issuerPort),
		http: &http.Client{
			Transport:     tr,
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		tokens: map[string]string{},
	}
}

// token signs in as who through the mock issuer's code flow with PKCE,
// the way the BFF does, and returns the API access token.
func (c *apiClient) token(ctx context.Context, t *testing.T, who string) string {
	t.Helper()
	if tok, ok := c.tokens[who]; ok {
		return tok
	}
	const redirect = "https://boop.example.com/auth/callback"
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"redirect_uri": {redirect}, "state": {"s"}, "nonce": {"n"}, "audience": {apiAudience},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.issuer+"/authorize?"+q.Encode(), http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "mock_user", Value: who})
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	res.Body.Close()
	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("authorize redirect %q: %v", res.Header.Get("Location"), err)
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "code_verifier": {verifier},
		"redirect_uri": {redirect}, "client_id": {"boopd-ui"}, "client_secret": {"client-secret"},
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.issuer+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err = c.http.Do(req)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer res.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.AccessToken == "" {
		t.Fatalf("token response %d: %v", res.StatusCode, err)
	}
	c.tokens[who] = body.AccessToken
	return body.AccessToken
}

// call sends method path to the API as who ("" for no token) and returns
// the status and body.
func (c *apiClient) call(ctx context.Context, t *testing.T, who, method, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if who != "" {
		req.Header.Set("Authorization", "Bearer "+c.token(ctx, t, who))
	}
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, b
}

// apiRecheckAudited (TestAPI_RecheckAudited): an operator's recheck
// starts a run, and action.recheck names the operator.
func apiRecheckAudited(ctx context.Context, t *testing.T, h *storeHarness, api *apiClient, repoID int64) {
	t.Helper()
	before := time.Now()
	status, body := api.call(ctx, t, "operator", http.MethodPost, fmt.Sprintf("/repositories/%d/recheck", repoID))
	if status != http.StatusAccepted {
		t.Fatalf("recheck = %d %s, want 202", status, body)
	}
	h.waitRun(ctx, t, repoID, before, 3*time.Minute)
	eventually(t, time.Minute, "action.recheck with the operator", func() bool {
		return h.count(ctx, t, "events", fmt.Sprintf(
			"kind = 'action.recheck' AND repo_id = %d AND actor_login IS NOT NULL AND actor_login <> ''", repoID)) >= 1
	})
	if status, _ := api.call(
		ctx,
		t,
		"viewer",
		http.MethodPost,
		fmt.Sprintf("/repositories/%d/recheck", repoID),
	); status != http.StatusNotFound &&
		status != http.StatusForbidden {
		t.Errorf("viewer recheck = %d, want 404 or 403", status)
	}
}

// apiVisibility (TestAPI_Visibility): a repository under an owner the
// caller cannot see answers 404, like an unknown one.
func apiVisibility(ctx context.Context, t *testing.T, api *apiClient, repoID int64) {
	t.Helper()
	path := fmt.Sprintf("/repositories/%d", repoID)
	if status, body := api.call(ctx, t, "operator", http.MethodGet, path); status != http.StatusOK {
		t.Errorf("operator GET %s = %d %s, want 200", path, status, body)
	}
	if status, _ := api.call(ctx, t, "viewer", http.MethodGet, path); status != http.StatusNotFound {
		t.Errorf("viewer GET %s = %d, want 404", path, status)
	}
	if status, _ := api.call(ctx, t, "viewer", http.MethodGet, "/repositories/999999999"); status != http.StatusNotFound {
		t.Errorf("viewer GET an unknown repository = %d, want 404", status)
	}
	if status, _ := api.call(ctx, t, "", http.MethodGet, path); status != http.StatusUnauthorized {
		t.Errorf("anonymous GET %s = %d, want 401", path, status)
	}
}

// apiStatusAggregateOnly (TestAPI_StatusAggregateOnly): /status answers
// without a token and names no owner or repository.
func apiStatusAggregateOnly(ctx context.Context, t *testing.T, api *apiClient, slug string) {
	t.Helper()
	status, body := api.call(ctx, t, "", http.MethodGet, "/status")
	if status != http.StatusOK {
		t.Fatalf("GET /status = %d %s, want 200", status, body)
	}
	owner, _, _ := strings.Cut(slug, "/")
	for _, name := range []string{slug, owner, "acme"} {
		if strings.Contains(string(body), name) {
			t.Errorf("/status names %q: %s", name, body)
		}
	}
}
