// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package docsite

import (
	"html/template"
	"strings"
)

// highlightJSON renders raw (already pretty-printed, exactly as authored)
// JSON text as HTML with syntax-coloring spans, preserving every character
// of the input otherwise verbatim - whitespace, line breaks, and indentation
// are never reformatted, since callers rely on the input already being
// formatted the way it should render (see model's exampleJSON).
//
// This walks the raw text with a small hand-rolled tokenizer rather than
// unmarshaling into a Go value and re-serializing: round-tripping through
// encoding/json's map[string]any would both lose field order (Go map
// iteration is unordered, and json.Marshal sorts map keys alphabetically)
// and reformat whitespace, neither of which is acceptable here. raw is
// assumed to already be valid JSON - examples_test.go enforces that for
// every value this is ever called with - so this does not itself validate;
// a malformed byte just falls through to the default case below.
func highlightJSON(raw string) template.HTML {
	var b strings.Builder
	i, n := 0, len(raw)
	for i < n {
		c := raw[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			j := i
			for j < n && isJSONSpace(raw[j]) {
				j++
			}
			b.WriteString(raw[i:j]) // whitespace never needs HTML-escaping.
			i = j
		case c == '{' || c == '}' || c == '[' || c == ']' || c == ':' || c == ',':
			writeSpan(&b, "jpunct", raw[i:i+1])
			i++
		case c == '"':
			j := endOfJSONString(raw, i)
			class := "jstring"
			if isJSONKey(raw, j) {
				class = "jkey"
			}
			writeSpan(&b, class, raw[i:j])
			i = j
		case c == '-' || (c >= '0' && c <= '9'):
			j := endOfJSONNumber(raw, i)
			writeSpan(&b, "jnumber", raw[i:j])
			i = j
		case strings.HasPrefix(raw[i:], "true"):
			writeSpan(&b, "jconst", "true")
			i += 4
		case strings.HasPrefix(raw[i:], "false"):
			writeSpan(&b, "jconst", "false")
			i += 5
		case strings.HasPrefix(raw[i:], "null"):
			writeSpan(&b, "jconst", "null")
			i += 4
		default:
			// Shouldn't happen for well-formed JSON: emit verbatim,
			// HTML-escaped, rather than silently dropping or panicking.
			template.HTMLEscape(&b, []byte(raw[i:i+1]))
			i++
		}
	}
	return template.HTML(b.String())
}

func writeSpan(b *strings.Builder, class, text string) {
	b.WriteString(`<span class="`)
	b.WriteString(class)
	b.WriteString(`">`)
	template.HTMLEscape(b, []byte(text))
	b.WriteString(`</span>`)
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// endOfJSONString returns the index just past the closing quote of the JSON
// string literal starting at raw[start] (which must be '"'), honoring
// backslash escapes so an escaped quote doesn't end the string early.
func endOfJSONString(raw string, start int) int {
	i := start + 1
	for i < len(raw) {
		switch raw[i] {
		case '\\':
			i += 2 // Skip the escaped character too, whatever it is.
			continue
		case '"':
			return i + 1
		}
		i++
	}
	return i // Unterminated string (malformed input): consume to the end.
}

func endOfJSONNumber(raw string, start int) int {
	i := start
	for i < len(raw) {
		switch c := raw[i]; {
		case c >= '0' && c <= '9', c == '-', c == '+', c == '.', c == 'e', c == 'E':
			i++
		default:
			return i
		}
	}
	return i
}

// isJSONKey reports whether the string literal ending just before
// raw[afterCloseQuote] is an object key: the next non-whitespace character
// after it is ':'.
func isJSONKey(raw string, afterCloseQuote int) bool {
	i := afterCloseQuote
	for i < len(raw) && isJSONSpace(raw[i]) {
		i++
	}
	return i < len(raw) && raw[i] == ':'
}
