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

// Package distscan reads the facts boopd's floor depends on from a
// Renovate build's dist directory (/usr/local/renovate/dist in the
// image): the allowedUnsafeExecutions values, every manager, the
// managers whose artifact code can reach util/exec, and each manager's
// default managerFilePatterns (IMPL-0006 Phase 2). Render turns them into
// internal/renovate/unsafe's generated file.
//
// It reads the bundled JavaScript as text and never runs it.
package distscan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"
)

// Data is what one Renovate build says about unsafe executions.
type Data struct {
	// Image is the image reference the dist came from (recorded, not read).
	Image string
	// Version is Renovate's version, from dist/../package.json.
	Version string
	// Gated is allowedUnsafeExecutions' allowedValues.
	Gated []string
	// Managers is every manager directory under dist/modules/manager.
	Managers []string
	// ExecManagers are the managers whose artifacts.js reaches
	// util/exec/index.js through relative imports.
	ExecManagers []string
	// FilePatterns is each manager's default managerFilePatterns; a
	// manager whose defaultConfig has none is absent.
	FilePatterns map[string][]string
	// DynamicPatterns are managers whose defaultConfig builds its
	// managerFilePatterns at run time (a template literal), so they have
	// no static list here.
	DynamicPatterns []string
}

// errNotStatic marks an array literal holding something other than
// double-quoted strings.
var errNotStatic = errors.New("not a static string array")

// Scan reads dist and returns its Data with image recorded as given.
func Scan(dist, image string) (Data, error) {
	d := Data{Image: image, FilePatterns: map[string][]string{}}

	var err error
	if d.Version, err = version(dist); err != nil {
		return Data{}, err
	}
	if d.Gated, err = gated(dist); err != nil {
		return Data{}, err
	}

	managerDir := filepath.Join(dist, "modules", "manager")
	entries, err := os.ReadDir(managerDir)
	if err != nil {
		return Data{}, fmt.Errorf("distscan: listing managers: %w", err)
	}

	g := importGraph{files: map[string][]string{}}
	execTarget := filepath.Join(dist, "util", "exec", "index.js")
	for _, e := range entries {
		index := filepath.Join(managerDir, e.Name(), "index.js")
		if !e.IsDir() || !exists(index) {
			continue
		}
		name := e.Name()
		d.Managers = append(d.Managers, name)

		patterns, ok, err := filePatterns(index)
		switch {
		case errors.Is(err, errNotStatic):
			d.DynamicPatterns = append(d.DynamicPatterns, name)
		case err != nil:
			return Data{}, fmt.Errorf("distscan: manager %s: %w", name, err)
		case ok:
			d.FilePatterns[name] = patterns
		}

		artifacts := filepath.Join(managerDir, name, "artifacts.js")
		if exists(artifacts) && g.reaches(artifacts, execTarget) {
			d.ExecManagers = append(d.ExecManagers, name)
		}
	}
	if len(d.Managers) == 0 {
		return Data{}, fmt.Errorf("distscan: no managers under %s", managerDir)
	}
	slices.Sort(d.Managers)
	slices.Sort(d.ExecManagers)
	slices.Sort(d.DynamicPatterns)
	return d, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func version(dist string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dist, "..", "package.json"))
	if err != nil {
		return "", fmt.Errorf("distscan: reading package.json: %w", err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil || pkg.Version == "" {
		return "", fmt.Errorf("distscan: package.json has no version: %w", err)
	}
	return pkg.Version, nil
}

func gated(dist string) ([]string, error) {
	path := filepath.Join(dist, "config", "options", "index.js")
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("distscan: reading options: %w", err)
	}
	s := string(src)
	at := strings.Index(s, `name: "allowedUnsafeExecutions"`)
	if at < 0 {
		return nil, errors.New("distscan: option allowedUnsafeExecutions not found")
	}
	values, ok, err := arrayAfter(s[at:], "allowedValues:")
	if err != nil {
		return nil, fmt.Errorf("distscan: allowedUnsafeExecutions: %w", err)
	}
	if !ok {
		return nil, errors.New("distscan: allowedUnsafeExecutions has no allowedValues")
	}
	slices.Sort(values)
	return values, nil
}

// filePatterns returns the managerFilePatterns inside index.js's
// defaultConfig; ok is false when defaultConfig sets none.
func filePatterns(index string) (patterns []string, ok bool, err error) {
	src, err := os.ReadFile(index)
	if err != nil {
		return nil, false, err
	}
	s := string(src)
	at := strings.Index(s, "const defaultConfig = {")
	if at < 0 {
		return nil, false, nil
	}
	body, err := objectBody(s[at+len("const defaultConfig = "):])
	if err != nil {
		return nil, false, err
	}
	return arrayAfter(body, "managerFilePatterns:")
}

// objectBody returns s from its leading '{' to the matching '}',
// skipping braces inside string literals.
func objectBody(s string) (string, error) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'', '`':
			end, err := skipString(s, i)
			if err != nil {
				return "", err
			}
			i = end
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i+1], nil
			}
		}
	}
	return "", errors.New("unterminated object")
}

// arrayAfter finds key in s and parses the array literal after it, which
// must hold only double-quoted strings.
func arrayAfter(s, key string) (values []string, ok bool, err error) {
	_, after, found := strings.Cut(s, key)
	if !found {
		return nil, false, nil
	}
	rest := strings.TrimLeft(after, " \t\r\n")
	if !strings.HasPrefix(rest, "[") {
		return nil, false, fmt.Errorf("%s is not an array literal: %w", key, errNotStatic)
	}
	values = []string{}
	for i := 1; i < len(rest); i++ {
		switch rest[i] {
		case ']':
			return values, true, nil
		case '"':
			end, err := skipString(rest, i)
			if err != nil {
				return nil, false, err
			}
			v, err := unquote(rest[i+1 : end])
			if err != nil {
				return nil, false, err
			}
			values = append(values, v)
			i = end
		case ',', ' ', '\t', '\r', '\n':
		default:
			return nil, false, fmt.Errorf("%s holds %q: %w", key, rest[i:min(i+20, len(rest))], errNotStatic)
		}
	}
	return nil, false, fmt.Errorf("%s: unterminated array", key)
}

// skipString returns the index of the quote closing the literal opened
// at s[start].
func skipString(s string, start int) (int, error) {
	q := s[start]
	for i := start + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case q:
			return i, nil
		}
	}
	return 0, errors.New("unterminated string literal")
}

// unquote decodes the escapes a bundler emits in a double-quoted string.
func unquote(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i == len(s) {
			return "", errors.New("trailing backslash")
		}
		switch s[i] {
		case '\\', '"', '\'', '/':
			b.WriteByte(s[i])
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		default:
			return "", fmt.Errorf("unsupported escape \\%c", s[i])
		}
	}
	return b.String(), nil
}

var importRe = regexp.MustCompile(`(?:from|import)\s*"(\.{1,2}/[^"]+\.js)"`)

// importGraph follows relative ESM imports between dist files.
type importGraph struct {
	files map[string][]string
}

func (g importGraph) imports(file string) []string {
	if deps, ok := g.files[file]; ok {
		return deps
	}
	src, err := os.ReadFile(file)
	if err != nil {
		g.files[file] = nil
		return nil
	}
	var deps []string
	for _, m := range importRe.FindAllStringSubmatch(string(src), -1) {
		deps = append(deps, filepath.Join(filepath.Dir(file), m[1]))
	}
	g.files[file] = deps
	return deps
}

func (g importGraph) reaches(from, target string) bool {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f == target {
			return true
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		stack = append(stack, g.imports(f)...)
	}
	return false
}

var generated = template.Must(template.New("zz").Parse(`// Code generated by renovate-unsafe-gen from {{.Image}} (Renovate {{.Version}}). DO NOT EDIT.

package unsafe

// SourceImage is the Renovate image this file was generated from.
const SourceImage = {{printf "%q" .Image}}

// RenovateVersion is that image's Renovate version.
const RenovateVersion = {{printf "%q" .Version}}

// gated is allowedUnsafeExecutions' allowedValues.
var gated = []string{ {{- range .Gated}}{{printf "%q" .}}, {{end -}} }

// managers is every Renovate manager.
var managers = []string{
{{- range .Managers}}
	{{printf "%q" .}},
{{- end}}
}

// execManagers are the managers whose artifact code reaches util/exec.
var execManagers = []string{
{{- range .ExecManagers}}
	{{printf "%q" .}},
{{- end}}
}

// dynamicPatterns are managers whose default managerFilePatterns are
// built at run time, so defaultFilePatterns has no entry for them.
var dynamicPatterns = []string{ {{- range .DynamicPatterns}}{{printf "%q" .}}, {{end -}} }

// defaultFilePatterns is each manager's default managerFilePatterns.
var defaultFilePatterns = map[string][]string{
{{- range $m, $p := .FilePatterns}}
	{{printf "%q" $m}}: { {{- range $p}}{{printf "%q" .}}, {{end -}} },
{{- end}}
}
`))

// Render returns d as internal/renovate/unsafe's zz_generated.go,
// gofmt-formatted.
func Render(d *Data) ([]byte, error) {
	var buf bytes.Buffer
	if err := generated.Execute(&buf, d); err != nil {
		return nil, fmt.Errorf("distscan: rendering: %w", err)
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("distscan: formatting: %w", err)
	}
	return out, nil
}
