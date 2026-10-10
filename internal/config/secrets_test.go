package config_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/boop/internal/config"
)

func loadExample(t *testing.T) *config.Config {
	t.Helper()
	cfg, diags := config.Load(examplePath)
	if diags.HasErrors() {
		t.Fatalf("Load(%s): %v", examplePath, diags.Error())
	}
	return cfg
}

func writeSecret(t *testing.T, dir, name, key, value string) {
	t.Helper()
	p := filepath.Join(dir, name, key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadSecrets(t *testing.T) {
	t.Parallel()
	cfg := loadExample(t)
	cfg.SecretsDir = t.TempDir()
	writeSecret(t, cfg.SecretsDir, "boop-bot-app", "private-key.pem", "-----BEGIN RSA PRIVATE KEY-----\nxyz\n")
	writeSecret(t, cfg.SecretsDir, "boopd-redis", "url", "redis://boopd-redis:6379\n")

	if err := cfg.ReadSecrets(); err != nil {
		t.Fatalf("ReadSecrets() = %v", err)
	}
	if got := cfg.Apps[0].PrivateKey.Reveal(); !strings.HasPrefix(got, "-----BEGIN") {
		t.Errorf("PrivateKey = %q, want the PEM", got)
	}
	if got := cfg.Renovate.RedisURL.Reveal(); got != "redis://boopd-redis:6379" {
		t.Errorf("RedisURL = %q, want trimmed redis://boopd-redis:6379", got)
	}
}

func TestReadSecrets_NeverFromEnv(t *testing.T) {
	// Not parallel: sets the environment.
	t.Setenv("REDIS_URL", "redis://from-env")
	t.Setenv("RENOVATE_REDIS_URL", "redis://from-env")
	cfg := loadExample(t)
	cfg.SecretsDir = t.TempDir()
	writeSecret(t, cfg.SecretsDir, "boop-bot-app", "private-key.pem", "pem")

	err := cfg.ReadSecrets()
	if err == nil {
		t.Fatal("ReadSecrets() without the redis file = nil, want error")
	}
	if !cfg.Renovate.RedisURL.IsZero() {
		t.Errorf("RedisURL = %q, want unset; the environment must not be consulted", cfg.Renovate.RedisURL.Reveal())
	}
}

func TestReadSecrets_MissingFilesNamePaths(t *testing.T) {
	t.Parallel()
	cfg := loadExample(t)
	cfg.SecretsDir = t.TempDir()
	writeSecret(t, cfg.SecretsDir, "boopd-redis", "url", "   \n")

	err := cfg.ReadSecrets()
	if err == nil {
		t.Fatal("ReadSecrets() = nil, want error")
	}
	for _, want := range []string{
		filepath.Join(cfg.SecretsDir, "boop-bot-app", "private-key.pem"),
		filepath.Join(cfg.SecretsDir, "boopd-redis", "url") + ": file is empty",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ReadSecrets() = %v, want it to name %s", err, want)
		}
	}
}

func TestSecret_Redacts(t *testing.T) {
	t.Parallel()
	cfg := loadExample(t)
	cfg.SecretsDir = t.TempDir()
	writeSecret(t, cfg.SecretsDir, "boop-bot-app", "private-key.pem", "TOPSECRET")
	writeSecret(t, cfg.SecretsDir, "boopd-redis", "url", "redis://TOPSECRET")
	if err := cfg.ReadSecrets(); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("x", "key", cfg.Apps[0].PrivateKey, "app", cfg.Apps[0])
	js, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"%v":   fmt.Sprintf("%v", cfg.Apps[0]),
		"%+v":  fmt.Sprintf("%+v", cfg.Renovate),
		"%#v":  fmt.Sprintf("%#v", cfg.Apps[0].PrivateKey),
		"slog": logs.String(),
		"json": string(js),
	} {
		if strings.Contains(out, "TOPSECRET") {
			t.Errorf("%s output leaks the secret: %s", name, out)
		}
	}
}
