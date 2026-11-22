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

package purl_test

import (
	"testing"

	"github.com/donaldgifford/boop/internal/purl"
)

func TestFromRenovate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		datasource, name, version string
		want                      string
		ok                        bool
	}{
		// DESIGN-0004's table.
		{"go", "golang.org/x/crypto", "v0.57.0", "pkg:golang/golang.org/x/crypto@v0.57.0", true},
		{"npm", "@types/node", "22.1.0", "pkg:npm/%40types/node@22.1.0", true},
		{"pypi", "requests", "2.32.3", "pkg:pypi/requests@2.32.3", true},
		{"docker", "golang", "1.27.2", "pkg:docker/library/golang@1.27.2", true},
		{"github-actions", "actions/checkout", "v6", "pkg:github/actions/checkout@v6", true},
		{"maven", "org.slf4j:slf4j-api", "2.0.13", "pkg:maven/org.slf4j/slf4j-api@2.0.13", true},
		{"crate", "serde", "1.0.210", "pkg:cargo/serde@1.0.210", true},
		{"rubygems", "rails", "7.2.1", "pkg:gem/rails@7.2.1", true},
		{"nuget", "Newtonsoft.Json", "13.0.3", "pkg:nuget/Newtonsoft.Json@13.0.3", true},

		// Encoding and normalisation.
		{"go", "github.com/foo/bar", "v2.0.0+incompatible", "pkg:golang/github.com/foo/bar@v2.0.0%2Bincompatible", true},
		{"npm", "left-pad", "", "pkg:npm/left-pad", true},
		{"pypi", "Typing_Extensions", "4.12.2", "pkg:pypi/typing-extensions@4.12.2", true},
		{"github-tags", "Kubernetes/Kubernetes", "v1.31.0", "pkg:github/kubernetes/kubernetes@v1.31.0", true},
		{"github-releases", "cli/cli", "v2.60.0", "pkg:github/cli/cli@v2.60.0", true},
		{"github-actions", "actions/cache/save", "v4", "pkg:github/actions/cache@v4#save", true},

		// Docker: namespaces, registries and digests.
		{"docker", "grafana/grafana", "11.3.0", "pkg:docker/grafana/grafana@11.3.0", true},
		{"docker", "docker.io/library/redis", "7.4", "pkg:docker/library/redis@7.4", true},
		{"docker", "ghcr.io/renovatebot/renovate", "43.1.0", "pkg:docker/renovatebot/renovate@43.1.0?repository_url=ghcr.io", true},
		{"docker", "localhost:5000/app", "1", "pkg:docker/app@1?repository_url=localhost:5000", true},
		{
			"docker", "golang", "sha256:0123abcd",
			"pkg:oci/golang@sha256%3A0123abcd?repository_url=docker.io/library/golang", true,
		},
		{
			"docker", "ghcr.io/renovatebot/renovate", "sha256:ff",
			"pkg:oci/renovate@sha256%3Aff?repository_url=ghcr.io/renovatebot/renovate", true,
		},

		// No purl type.
		{"helm", "cert-manager", "v1.16.0", "", false},
		{"git-refs", "https://example.com/x.git", "abc", "", false},
		{"go", "", "v1", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.datasource+"/"+tt.name+"@"+tt.version, func(t *testing.T) {
			t.Parallel()
			got, ok := purl.FromRenovate(tt.datasource, tt.name, tt.version)
			if got != tt.want || ok != tt.ok {
				t.Errorf("FromRenovate(%q, %q, %q) = %q, %v; want %q, %v", tt.datasource, tt.name, tt.version, got, ok, tt.want, tt.ok)
			}
		})
	}
}
