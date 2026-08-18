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
	"bytes"
	"html/template"
	"strings"

	"github.com/yuin/goldmark"
)

// markdownRenderer renders proto comments (message/field/method/enum doc
// comments) as HTML: paragraphs, lists, emphasis, inline code, and links,
// per plain CommonMark. goldmark.New() with no extensions is deliberate -
// no GFM tables, strikethrough, or autolinked bare URLs, none of which fit
// a field description anyway. Comments already tend to use Markdown-ish
// conventions by hand (e.g. a "- some_field" bullet list to enumerate
// supported values), so this mostly formalizes what authors already write
// rather than asking for anything new.
//
// Raw HTML in a comment is left escaped rather than passed through, since
// html.WithUnsafe() is not set: comments come from the same trusted proto
// source as everything else this generator renders, but there's no reason
// to carve out an exception to normal escaping for them specifically.
var markdownRenderer = goldmark.New()

func markdown(comment string) (template.HTML, error) {
	// Pre-escape rather than let goldmark's own (non-unsafe) handling of raw
	// HTML deal with it: CommonMark treats anything shaped like a tag (e.g.
	// "<name>" written as a placeholder, not an actual tag) as raw HTML, and
	// goldmark's safe mode silently drops it - "the field <name> must be
	// set" would render as "the field  must be set", quietly eating real
	// content. Escaping first means goldmark only ever sees literal text
	// there, which it then renders back out verbatim (still safe, since the
	// HTML renderer escapes everything it writes) - all without touching
	// real Markdown syntax (lists, emphasis, code spans, links, "&" in
	// prose), none of which uses these three characters unescaped.
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(comment)

	var buf bytes.Buffer
	if err := markdownRenderer.Convert([]byte(escaped), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}
