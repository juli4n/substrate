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
	"testing"
)

func TestHighlightJSON(t *testing.T) {
	raw := `{
  "name": "prod",
  "count": 3,
  "active": true,
  "missing": null,
  "tags": ["a", "b"]
}`
	got := string(highlightJSON(raw))

	// Whitespace/structure must survive verbatim - stripping every span tag
	// should reproduce the input exactly, once the input's own quotes are
	// escaped the same way template.HTMLEscape escapes everything it wraps.
	stripped := got
	for _, tag := range []string{
		`<span class="jkey">`, `<span class="jstring">`, `<span class="jnumber">`,
		`<span class="jconst">`, `<span class="jpunct">`, `</span>`,
	} {
		stripped = strings.ReplaceAll(stripped, tag, "")
	}
	if want := template.HTMLEscapeString(raw); stripped != want {
		t.Errorf("stripping span tags did not reproduce the (escaped) input.\ngot:  %q\nwant: %q", stripped, want)
	}

	for _, want := range []string{
		`<span class="jkey">&#34;name&#34;</span>`,
		`<span class="jstring">&#34;prod&#34;</span>`,
		`<span class="jnumber">3</span>`,
		`<span class="jconst">true</span>`,
		`<span class="jconst">null</span>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %s\nfull output:\n%s", want, got)
		}
	}

	// "count" is a key (followed by ':') even though "b" right after it in
	// the tags array is a plain value, not a key - guards against a naive
	// "every other string is a key" mistake.
	if !strings.Contains(got, `<span class="jstring">&#34;b&#34;</span>`) {
		t.Error(`"b" (an array element, not a key) was rendered as a key`)
	}
}

func TestHighlightJSON_EscapesHTML(t *testing.T) {
	raw := `{"comment": "<script>alert(1)</script> & \"quoted\""}`
	got := string(highlightJSON(raw))
	if strings.Contains(got, "<script>alert(1)</script>") {
		t.Error("output contains the dangerous string unescaped")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Error("output does not contain the escaped form - content may have been dropped")
	}
}
