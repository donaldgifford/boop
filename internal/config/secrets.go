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

package config

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

const redacted = "[redacted]"

// Secret holds a value read from a mounted Secret file. It prints, logs
// and marshals as "[redacted]", so it cannot leak through a log line, a
// %v or a Temporal payload by accident; Bytes and Reveal are the only
// way to the value.
type Secret struct {
	value []byte
}

// NewSecret wraps a copy of b, for callers that hold the value already
// (tests, keys from elsewhere).
func NewSecret(b []byte) Secret { return Secret{value: bytes.Clone(b)} }

// Bytes returns a copy of the value.
func (s Secret) Bytes() []byte { return bytes.Clone(s.value) }

// Reveal returns the value as a string.
func (s Secret) Reveal() string { return string(s.value) }

// IsZero reports whether no value was read.
func (s Secret) IsZero() bool { return len(s.value) == 0 }

// String implements fmt.Stringer.
func (Secret) String() string { return redacted }

// GoString implements fmt.GoStringer.
func (Secret) GoString() string { return redacted }

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON implements json.Marshaler.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Path is where the chart mounts the ref: <dir>/<name>/<key>. Name and
// key are validated at load (a DNS subdomain and a Secret key), so
// neither can climb out of dir.
func (r SecretRef) Path(dir string) string {
	return filepath.Join(dir, r.Name, r.Key)
}

// ReadSecrets reads every secret-backed value from the files the refs
// name under SecretsDir: each App's private key and, when configured,
// the Redis URL. It never consults the environment. Call it once at
// worker start; a missing or empty file is an error naming its path.
// Every failure is reported.
func (c *Config) ReadSecrets() error {
	var errs []error
	for _, app := range c.Apps {
		v, err := readSecret(app.PrivateKeySecretRef.Path(c.SecretsDir))
		if err != nil {
			errs = append(errs, fmt.Errorf("app %q private key: %w", app.Name, err))
			continue
		}
		app.PrivateKey = v
	}
	if ref := c.Renovate.RedisSecretRef; ref != nil {
		v, err := readSecret(ref.Path(c.SecretsDir))
		if err != nil {
			errs = append(errs, fmt.Errorf("redis url: %w", err))
		} else {
			c.Renovate.RedisURL = Secret{value: bytes.TrimSpace(v.value)}
		}
	}
	return errors.Join(errs...)
}

func readSecret(path string) (Secret, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Secret{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return Secret{}, fmt.Errorf("read %s: file is empty", path)
	}
	return Secret{value: b}, nil
}
