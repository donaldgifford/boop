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

// Package purl maps a Renovate datasource and package name to a package
// URL (https://github.com/package-url/purl-spec), the identity a security
// correlator joins on (DESIGN-0004 § Package identity and the security
// join).
package purl

import (
	"strings"
)

// FromRenovate returns the purl of packageName at version for a Renovate
// datasource. version may be empty, for a purl without one. It returns
// false for a datasource with no purl type, or no package name.
func FromRenovate(datasource, packageName, version string) (string, bool) {
	name := strings.TrimSpace(packageName)
	if name == "" {
		return "", false
	}
	var p purl
	switch datasource {
	case "go":
		p = split("golang", name, "/")
	case "npm":
		p = split("npm", name, "/")
	case "pypi":
		p = purl{typ: "pypi", name: pypiName(name)}
	case "docker":
		return docker(name, version), true
	case "github-tags", "github-releases", "github-actions":
		p = github(name)
	case "maven":
		p = split("maven", name, ":")
	case "crate":
		p = purl{typ: "cargo", name: name}
	case "rubygems":
		p = purl{typ: "gem", name: name}
	case "nuget":
		p = purl{typ: "nuget", name: name}
	default:
		return "", false
	}
	p.version = version
	return p.String(), true
}

// purl is a package URL's parts, before encoding.
type purl struct {
	typ        string
	namespace  []string
	name       string
	version    string
	qualifiers [][2]string
	subpath    string
}

// String encodes p: each namespace segment, the name, version and
// qualifier values percent-encoded.
func (p purl) String() string {
	var b strings.Builder
	b.WriteString("pkg:")
	b.WriteString(p.typ)
	b.WriteByte('/')
	for _, ns := range p.namespace {
		b.WriteString(escape(ns))
		b.WriteByte('/')
	}
	b.WriteString(escape(p.name))
	if p.version != "" {
		b.WriteByte('@')
		b.WriteString(escape(p.version))
	}
	for i, q := range p.qualifiers {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(q[0])
		b.WriteByte('=')
		b.WriteString(escapeQualifier(q[1]))
	}
	if p.subpath != "" {
		b.WriteByte('#')
		b.WriteString(p.subpath)
	}
	return b.String()
}

// split takes the last sep-separated part of name as the name and the
// rest as the namespace.
func split(typ, name, sep string) purl {
	parts := strings.Split(name, sep)
	return purl{typ: typ, namespace: parts[:len(parts)-1], name: parts[len(parts)-1]}
}

// pypiName normalises a PyPI name: lower case, underscores as dashes.
func pypiName(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), "_", "-")
}

// github is owner/repo, lower case; anything after the repository, as in
// an action at a path, is the subpath.
func github(name string) purl {
	parts := strings.Split(strings.ToLower(name), "/")
	if len(parts) < 2 {
		return purl{typ: "github", name: parts[0]}
	}
	return purl{typ: "github", namespace: parts[:1], name: parts[1], subpath: strings.Join(parts[2:], "/")}
}

// docker returns a docker purl, or an oci one when version is a digest.
// Docker Hub images live under library/ when they have no namespace;
// other registries go in repository_url.
func docker(name, version string) string {
	registry, path := splitRegistry(strings.ToLower(name))
	if strings.HasPrefix(version, "sha256:") {
		repo := registry
		if repo == "" {
			repo = "docker.io"
		}
		segs := strings.Split(path, "/")
		return purl{
			typ: "oci", name: segs[len(segs)-1], version: version,
			qualifiers: [][2]string{{"repository_url", repo + "/" + path}},
		}.String()
	}
	p := split("docker", path, "/")
	p.version = version
	if registry != "" {
		p.qualifiers = [][2]string{{"repository_url", registry}}
	}
	return p.String()
}

// splitRegistry separates a registry host from an image path. Docker Hub
// is no registry, and its single-segment images are under library/.
func splitRegistry(name string) (registry, path string) {
	first, rest, ok := strings.Cut(name, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		registry, name = first, rest
	}
	switch registry {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		registry = ""
	default:
	}
	if registry == "" && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	return registry, name
}

// escape percent-encodes everything but the unreserved characters.
func escape(s string) string {
	return encode(s, unreserved)
}

// escapeQualifier also keeps '/' and ':', which a repository URL needs.
func escapeQualifier(s string) string {
	return encode(s, func(c byte) bool { return unreserved(c) || c == '/' || c == ':' })
}

func unreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

func encode(s string, keep func(byte) bool) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if keep(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}
