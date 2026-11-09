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

package unsafe

import (
	"slices"
	"testing"
)

func TestMatchFloor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		floor []string
		paths []string
		want  []string
	}{
		{name: "nothing", floor: Floor(), paths: []string{"go.mod", "README.md", "package.json"}},
		{name: "Gemfile at the root", floor: Floor(), paths: []string{"Gemfile", "Gemfile.lock"}, want: []string{"bundler"}},
		{name: "nested pyproject", floor: Floor(), paths: []string{"svc/api/pyproject.toml"}, want: []string{"pep621", "poetry"}},
		{name: "maven wrapper", floor: Floor(), paths: []string{"mvnw", ".mvn/wrapper/maven-wrapper.properties"}, want: []string{"maven-wrapper"}},
		{name: "csproj", floor: Floor(), paths: []string{"src/App/App.csproj"}, want: []string{"nuget"}},
		{name: "no partial name", floor: Floor(), paths: []string{"NotAGemfile", "Gemfile.bak"}},
		{name: "pip-compile has no default patterns", floor: Floor(), paths: []string{"requirements.in", "requirements.txt"}},
		{name: "floor_add", floor: append(Floor(), "gomod"), paths: []string{"go.mod"}, want: []string{"gomod"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchFloor(tt.floor, tt.paths); !slices.Equal(got, tt.want) {
				t.Errorf("MatchFloor(%v) = %v, want %v", tt.paths, got, tt.want)
			}
		})
	}
}

// Every static pattern of every manager compiles, so MatchFloor never
// skips one for a floor_add manager either.
func TestPatternsCompile(t *testing.T) {
	t.Parallel()
	for m, patterns := range defaultFilePatterns {
		for _, p := range patterns {
			if _, err := compilePattern(p); err != nil {
				t.Errorf("%s pattern %q: %v", m, p, err)
			}
		}
	}
}

func TestCompilePattern_Glob(t *testing.T) {
	t.Parallel()
	re, err := compilePattern("**/*.tf")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{"main.tf": true, "a/b/main.tf": true, "main.tfvars": false} {
		if got := re.MatchString(path); got != want {
			t.Errorf("**/*.tf matches %q = %v, want %v", path, got, want)
		}
	}
}
