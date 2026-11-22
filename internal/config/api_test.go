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

package config_test

import (
	"strings"
	"testing"

	"github.com/donaldgifford/boop/internal/config"
)

func TestAPI_Defaults(t *testing.T) {
	t.Parallel()
	cfg, diags := config.Parse("api.hcl", []byte(minimalApp+storeApp))
	if diags.HasErrors() {
		var b strings.Builder
		_, _ = diags.WriteTo(&b)
		t.Fatalf("Parse diagnostics:\n%s", b.String())
	}
	want := config.API{AuthzFile: config.DefaultAuthzFile, StatusPublic: true}
	if cfg.API != want {
		t.Errorf("api = %+v, want %+v", cfg.API, want)
	}
}

func TestAPI_Set(t *testing.T) {
	t.Parallel()
	src := minimalApp + `
api {
  auth {
    issuer   = "https://keycloak.example/realms/fartlab"
    audience = "boopd-api"
    name_claim = "name"
  }
  authz_file    = "/etc/boopd/authz/authz.yaml"
  status_public = false
}
` + storeApp
	cfg, diags := config.Parse("api.hcl", []byte(src))
	if diags.HasErrors() {
		var b strings.Builder
		_, _ = diags.WriteTo(&b)
		t.Fatalf("Parse diagnostics:\n%s", b.String())
	}
	want := config.APIAuth{
		Issuer: "https://keycloak.example/realms/fartlab", Audience: "boopd-api",
		NameClaim: "name", GroupsClaim: config.DefaultGroupsClaim,
	}
	if cfg.API.Auth == nil || *cfg.API.Auth != want {
		t.Errorf("api.auth = %+v, want %+v", cfg.API.Auth, want)
	}
	if cfg.API.AuthzFile != "/etc/boopd/authz/authz.yaml" || cfg.API.StatusPublic {
		t.Errorf("api = %+v, want the set authz_file and status_public false", cfg.API)
	}
}

func TestAPI_Validation(t *testing.T) {
	t.Parallel()
	src := minimalApp + `
api {
  auth {
    issuer   = "http://keycloak.example"
    audience = " "
  }
  authz_file = "authz.yaml"
}
` + storeApp
	_, diags := config.Parse("api.hcl", []byte(src))
	var b strings.Builder
	_, _ = diags.WriteTo(&b)
	for _, want := range []string{"must be an https URL (http only on loopback)", "audience must not be empty", "must be an absolute path"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("diagnostics do not mention %q:\n%s", want, b.String())
		}
	}
}

func TestAPI_LoopbackIssuer(t *testing.T) {
	t.Parallel()
	for _, issuer := range []string{"http://127.0.0.1:5556", "http://localhost:8080/realms/x", "http://[::1]:9000"} {
		src := minimalApp + `
api {
  auth {
    issuer   = "` + issuer + `"
    audience = "boopd-api"
  }
}
` + storeApp
		if _, diags := config.Parse("api.hcl", []byte(src)); diags.HasErrors() {
			var b strings.Builder
			_, _ = diags.WriteTo(&b)
			t.Errorf("issuer %s: diagnostics:\n%s", issuer, b.String())
		}
	}
}
