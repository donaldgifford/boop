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

package github

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// json5ToJSON rewrites JSON5 text as JSON so encoding/json can decode it.
// Renovate reads its config files as JSON, JSONC or JSON5, and the first
// two are subsets of the third, so one rewrite serves every file name.
// It handles what JSON5 adds to JSON: comments, single-quoted strings and
// their extra escapes, unquoted keys, trailing commas, and the number
// forms JSON lacks (hex, a leading or trailing point, a plus sign).
// Infinity and NaN become null. Structure is not checked here;
// json.Unmarshal does that.
func json5ToJSON(src string) (string, error) {
	s := json5Scanner{src: src}
	s.out.Grow(len(src))
	for {
		s.skipSpace()
		if s.err != nil {
			return "", s.err
		}
		if s.i >= len(s.src) {
			return s.out.String(), nil
		}
		s.token()
		if s.err != nil {
			return "", s.err
		}
	}
}

type json5Scanner struct {
	src string
	i   int
	out strings.Builder
	err error
}

func (s *json5Scanner) fail(format string, args ...any) {
	if s.err == nil {
		s.err = fmt.Errorf("json5: offset %d: %s", s.i, fmt.Sprintf(format, args...))
	}
}

// skipSpace skips whitespace and comments, which JSON5 allows between
// any two tokens.
func (s *json5Scanner) skipSpace() {
	for s.i < len(s.src) {
		rest := s.src[s.i:]
		r, n := utf8.DecodeRuneInString(rest)
		switch {
		case unicode.IsSpace(r) || r == '\uFEFF':
			s.i += n
		case strings.HasPrefix(rest, "//"):
			end := strings.IndexAny(rest, "\n\r\u2028\u2029")
			if end < 0 {
				s.i = len(s.src)
				return
			}
			s.i += end
		case strings.HasPrefix(rest, "/*"):
			end := strings.Index(rest[2:], "*/")
			if end < 0 {
				s.fail("unterminated comment")
				return
			}
			s.i += end + 4
		default:
			return
		}
	}
}

// peek is the byte after any whitespace and comments, without consuming
// them; 0 at the end of input.
func (s *json5Scanner) peek() byte {
	save := s.i
	s.skipSpace()
	var b byte
	if s.i < len(s.src) {
		b = s.src[s.i]
	}
	s.i = save
	return b
}

func (s *json5Scanner) token() {
	c := s.src[s.i]
	switch {
	case c == ',':
		s.i++
		// A trailing comma before a closing bracket is dropped.
		if p := s.peek(); p != '}' && p != ']' {
			s.out.WriteByte(',')
		}
	case strings.IndexByte("{}[]:", c) >= 0:
		s.out.WriteByte(c)
		s.i++
	case c == '"' || c == '\'':
		s.quoted(c)
	case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
		s.number()
	default:
		s.word()
	}
}

// quoted reads a quoted string and writes it JSON-encoded.
func (s *json5Scanner) quoted(quote byte) {
	s.i++
	var b strings.Builder
	for s.i < len(s.src) {
		c := s.src[s.i]
		switch c {
		case quote:
			s.i++
			s.writeString(b.String())
			return
		case '\\':
			s.i++
			s.escape(&b)
			if s.err != nil {
				return
			}
		case '\n', '\r':
			s.fail("newline in string")
			return
		default:
			b.WriteByte(c)
			s.i++
		}
	}
	s.fail("unterminated string")
}

// escape decodes one escape sequence after its backslash.
func (s *json5Scanner) escape(b *strings.Builder) {
	if s.i >= len(s.src) {
		s.fail("unterminated escape")
		return
	}
	r, n := utf8.DecodeRuneInString(s.src[s.i:])
	s.i += n
	switch r {
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'v':
		b.WriteByte('\v')
	case '0':
		b.WriteByte(0)
	case 'x':
		s.hexRune(b, 2)
	case 'u':
		s.hexRune(b, 4)
	case '\r':
		// A line continuation adds nothing; CRLF counts as one break.
		if s.i < len(s.src) && s.src[s.i] == '\n' {
			s.i++
		}
	case '\n', '\u2028', '\u2029':
	default:
		// Any other character escapes to itself: \' \" \\ \/ and the rest.
		b.WriteRune(r)
	}
}

func (s *json5Scanner) hexRune(b *strings.Builder, digits int) {
	if s.i+digits > len(s.src) {
		s.fail("short hex escape")
		return
	}
	var v rune
	for _, c := range s.src[s.i : s.i+digits] {
		var d rune
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			s.fail("bad hex escape %q", s.src[s.i:s.i+digits])
			return
		}
		v = v<<4 | d
	}
	s.i += digits
	b.WriteRune(v)
}

func (s *json5Scanner) writeString(v string) {
	enc, err := json.Marshal(v)
	if err != nil {
		s.fail("%v", err)
		return
	}
	s.out.Write(enc)
}

// number reads a number, or a signed Infinity or NaN, and writes its
// JSON form.
func (s *json5Scanner) number() {
	start := s.i
	if c := s.src[s.i]; c == '+' || c == '-' {
		s.i++
		if s.i < len(s.src) && (s.src[s.i] < '0' || s.src[s.i] > '9') && s.src[s.i] != '.' {
			if w := s.ident(); w != "Infinity" && w != "NaN" {
				s.fail("unexpected word %q", s.src[start:s.i])
				return
			}
			s.out.WriteString("null")
			return
		}
	}
	for s.i < len(s.src) && strings.IndexByte("0123456789abcdefABCDEFxX.+-", s.src[s.i]) >= 0 {
		s.i++
	}
	tok := s.src[start:s.i]
	if json.Valid([]byte(tok)) {
		s.out.WriteString(tok)
		return
	}
	norm, err := json5Number(tok)
	if err != nil {
		s.fail("bad number %q", tok)
		return
	}
	s.out.WriteString(norm)
}

// json5Number spells a number JSON lacks (hex, a leading or trailing
// point, a plus sign) the JSON way.
func json5Number(tok string) (string, error) {
	digits := strings.TrimLeft(tok, "+-")
	if strings.HasPrefix(digits, "0x") || strings.HasPrefix(digits, "0X") {
		v, err := strconv.ParseInt(strings.TrimPrefix(tok, "+"), 0, 64)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(v, 10), nil
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return "", err
	}
	return strconv.FormatFloat(f, 'g', -1, 64), nil
}

// word reads an unquoted key or one of the literal words.
func (s *json5Scanner) word() {
	w := s.ident()
	if w == "" {
		r, _ := utf8.DecodeRuneInString(s.src[s.i:])
		s.fail("unexpected %q", r)
		return
	}
	if s.peek() == ':' {
		s.writeString(w)
		return
	}
	switch w {
	case "true", "false", "null":
		s.out.WriteString(w)
	case "Infinity", "NaN":
		s.out.WriteString("null")
	default:
		s.fail("unexpected word %q", w)
	}
}

// ident consumes an identifier ($, _, letters and digits) and returns
// it; empty when none starts here.
func (s *json5Scanner) ident() string {
	start := s.i
	for s.i < len(s.src) {
		r, n := utf8.DecodeRuneInString(s.src[s.i:])
		if r != '$' && r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		s.i += n
	}
	return s.src[start:s.i]
}
