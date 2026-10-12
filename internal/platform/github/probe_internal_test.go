package github

import (
	"slices"
	"strings"
	"testing"
)

func TestParseExtends(t *testing.T) {
	t.Parallel()

	big := `{"extends":["a"],"x":"` + strings.Repeat("y", maxConfigBytes) + `"}`
	tests := []struct {
		name     string
		text     string
		byteSize int
		want     []string
		wantErr  bool
	}{
		{name: "array", text: `{"extends":["config:recommended","github>o/p:python"]}`, want: []string{"config:recommended", "github>o/p:python"}},
		{name: "single string", text: `{"extends":"config:recommended"}`, want: []string{"config:recommended"}},
		{name: "empty array", text: `{"extends":[]}`, want: []string{}},
		{name: "absent", text: `{"labels":["deps"]}`},
		{name: "null", text: `{"extends":null}`},
		{name: "jsonc comment", text: "{\n// c\n\"extends\":[\"a\"]}", want: []string{"a"}},
		{
			name: "json5 as repo-guardian writes it",
			text: "{\n  $schema: \"https://docs.renovatebot.com/renovate-schema.json\",\n  extends: [\n    \"github>o/p\",\n    'github>o/p:go',\n  ],\n}\n",
			want: []string{"github>o/p", "github>o/p:go"},
		},
		{name: "empty text", text: "", wantErr: true},
		{name: "not json5", text: `extends: [a]`, wantErr: true},
		{name: "top-level array", text: `["a"]`, wantErr: true},
		{name: "wrong shape", text: `{"extends":{"a":1}}`, wantErr: true},
		{name: "mixed array", text: `{"extends":["a",1]}`, wantErr: true},
		{name: "over 64 KB by byteSize", text: `{"extends":["a"]}`, byteSize: maxConfigBytes + 1, wantErr: true},
		{name: "over 64 KB by text", text: big, wantErr: true},
		{name: "exactly 64 KB", text: `{"extends":["a"]}`, byteSize: maxConfigBytes, want: []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseExtends(tt.text, tt.byteSize)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseExtends(%.40q, %d) err = %v, wantErr %v", tt.text, tt.byteSize, err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) || (got == nil) != (tt.want == nil) {
				t.Errorf("parseExtends(%.40q, %d) = %q, want %q", tt.text, tt.byteSize, got, tt.want)
			}
		})
	}
}

func TestGraphqlURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		base string
		want string
	}{
		{base: "", want: "https://api.github.com/graphql"},
		{base: "https://api.github.com/", want: "https://api.github.com/graphql"},
		{base: "https://ghe.example.com/", want: "https://ghe.example.com/api/graphql"},
	}
	for _, tt := range tests {
		c, err := NewWithToken(TokenAuth{Token: "t", BaseURL: tt.base})
		if err != nil {
			t.Fatalf("NewWithToken(%q): %v", tt.base, err)
		}
		if got := c.graphqlURL(); got != tt.want {
			t.Errorf("graphqlURL() for %q = %q, want %q", tt.base, got, tt.want)
		}
	}
}
