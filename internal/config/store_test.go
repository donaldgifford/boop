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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/config"
)

const storeApp = `
app "boop-bot" {
  app_id = 1
  private_key_secret_ref {
    name = "a"
    key  = "k"
  }
}
`

func TestStore_DefaultsOff(t *testing.T) {
	t.Parallel()
	cfg, diags := config.Parse("store.hcl", []byte(minimalApp+storeApp))
	if diags.HasErrors() {
		var b strings.Builder
		_, _ = diags.WriteTo(&b)
		t.Fatalf("Parse diagnostics:\n%s", b.String())
	}
	want := config.Store{Retention: config.Retention{
		Events:           config.DefaultRetainEvents,
		DeliveryPayloads: config.DefaultRetainDeliveryPayloads,
		Offboarded:       config.DefaultRetainOffboarded,
	}}
	if cfg.Store != want {
		t.Errorf("store = %+v, want disabled with default retention %+v", cfg.Store, want)
	}
}

func TestStore_Set(t *testing.T) {
	t.Parallel()
	src := minimalApp + `
store {
  enabled = true
  owner_dsn_secret_ref {
    name = "boopd-store"
    key  = "owner-dsn"
  }
  ro_dsn_secret_ref {
    name = "boopd-store"
    key  = "ro-dsn"
  }
  retention {
    events = "720h"
  }
}
` + storeApp
	cfg, diags := config.Parse("store.hcl", []byte(src))
	if diags.HasErrors() {
		var b strings.Builder
		_, _ = diags.WriteTo(&b)
		t.Fatalf("Parse diagnostics:\n%s", b.String())
	}
	s := cfg.Store
	if !s.Enabled {
		t.Error("enabled = false, want true")
	}
	if s.OwnerDSNSecretRef == nil || *s.OwnerDSNSecretRef != (config.SecretRef{Name: "boopd-store", Key: "owner-dsn"}) {
		t.Errorf("owner ref = %+v", s.OwnerDSNSecretRef)
	}
	if s.DSNSecretRef != nil || s.IngestDSNSecretRef != nil {
		t.Errorf("unset refs = %+v, %+v, want nil", s.DSNSecretRef, s.IngestDSNSecretRef)
	}
	if s.Retention.Events != 720*time.Hour || s.Retention.Offboarded != config.DefaultRetainOffboarded {
		t.Errorf("retention = %+v", s.Retention)
	}
}

func TestStore_Validation(t *testing.T) {
	t.Parallel()
	src := minimalApp + `
store {
  dsn_secret_ref {
    name = "Not_A_Name"
    key  = "dsn"
  }
  retention {
    events = "0s"
  }
}
` + storeApp
	_, diags := config.Parse("store.hcl", []byte(src))
	var b strings.Builder
	_, _ = diags.WriteTo(&b)
	for _, want := range []string{"store.dsn_secret_ref.name", "must be positive"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("diagnostics do not mention %q:\n%s", want, b.String())
		}
	}
}

func TestReadStoreDSN(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "boopd-store"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "boopd-store", "owner-dsn"), []byte("postgres://o@db/boopd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SecretsDir: dir}

	got, err := cfg.ReadStoreDSN(&config.SecretRef{Name: "boopd-store", Key: "owner-dsn"}, "store.owner_dsn_secret_ref")
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != "postgres://o@db/boopd" {
		t.Errorf("dsn = %q, want it trimmed", got.Reveal())
	}

	_, err = cfg.ReadStoreDSN(nil, "store.ro_dsn_secret_ref")
	if !errors.Is(err, config.ErrNoStoreDSN) || !strings.Contains(err.Error(), "store.ro_dsn_secret_ref") {
		t.Errorf("err = %v, want ErrNoStoreDSN naming the ref", err)
	}
}
