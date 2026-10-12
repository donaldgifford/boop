package github

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSON5ToJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string // compared as decoded JSON
	}{
		{name: "plain json", in: `{"a": [1, "two", true, null]}`, want: `{"a":[1,"two",true,null]}`},
		{name: "line comment", in: "{\"a\": 1 // one\n}", want: `{"a":1}`},
		{name: "comment at end of input", in: "{\"a\": 1}\n// done", want: `{"a":1}`},
		{name: "block comment", in: `{/* lead */ "a": /* mid */ 1}`, want: `{"a":1}`},
		{
			name: "comment markers in strings",
			in:   `{"a": "// not a comment", "b": "/* nor this */"}`,
			want: `{"a":"// not a comment","b":"/* nor this */"}`,
		},
		{name: "unquoted keys", in: `{$schema: "s", _x: 1, a1: 2}`, want: `{"$schema":"s","_x":1,"a1":2}`},
		{name: "unquoted key before comment and colon", in: "{a /* c */ : 1}", want: `{"a":1}`},
		{name: "keyword as key", in: `{true: 1, null: 2}`, want: `{"true":1,"null":2}`},
		{name: "single quotes", in: `{'a': 'it\'s "x"'}`, want: `{"a":"it's \"x\""}`},
		{name: "trailing commas", in: "{\"a\": [1, 2, ], \"b\": {\"c\": 1,},\n}", want: `{"a":[1,2],"b":{"c":1}}`},
		{name: "trailing comma before comment", in: "[1, // last\n]", want: `[1]`},
		{name: "escapes", in: `["\x41\u00e9\t\v\0", "\/\q"]`, want: `["Aé\t\u000b\u0000","/q"]`},
		{name: "line continuation", in: "[\"a\\\nb\", 'c\\\r\nd']", want: `["ab","cd"]`},
		{name: "hex", in: `[0x1F, -0xa, +0X10]`, want: `[31,-10,16]`},
		{name: "number forms", in: `[.5, 5., +1, -2.5e3, 1e-2]`, want: `[0.5,5,1,-2500,0.01]`},
		{name: "infinity and nan", in: `[Infinity, -Infinity, +Infinity, NaN, -NaN]`, want: `[null,null,null,null,null]`},
		{name: "bom and unicode space", in: "\uFEFF{\u00a0\"a\":\u20281}", want: `{"a":1}`},
		{name: "unicode key", in: `{café: 1}`, want: `{"café":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json5ToJSON(tt.in)
			if err != nil {
				t.Fatalf("json5ToJSON(%q) err = %v", tt.in, err)
			}
			var g, w any
			if err := json.Unmarshal([]byte(got), &g); err != nil {
				t.Fatalf("json5ToJSON(%q) = %q, not JSON: %v", tt.in, got, err)
			}
			if err := json.Unmarshal([]byte(tt.want), &w); err != nil {
				t.Fatalf("want %q is not JSON: %v", tt.want, err)
			}
			gb, _ := json.Marshal(g)
			wb, _ := json.Marshal(w)
			if !bytes.Equal(gb, wb) {
				t.Errorf("json5ToJSON(%q) = %s, want %s", tt.in, gb, wb)
			}
		})
	}
}

func TestJSON5ToJSON_Errors(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		`{"a": "unterminated`,
		`{"a": 'line` + "\n" + `break'}`,
		`{/* unterminated comment`,
		`{"a": "\x4"}`,
		`{"a": "\uZZZZ"}`,
		`{"a": undefined}`,
		`{"a": -foo}`,
		`{"a": 0xZZ}`,
		`{"a": 1.2.3}`,
		`{"a": #}`,
		`{"a": "\`,
	} {
		if got, err := json5ToJSON(in); err == nil {
			t.Errorf("json5ToJSON(%q) = %q, want an error", in, got)
		}
	}
}
