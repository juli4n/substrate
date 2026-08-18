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

package docsite_test

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/agent-substrate/substrate/tools/apitool/internal/docsite"
	"github.com/agent-substrate/substrate/tools/apitool/internal/model"
)

// fixtureAPI's method/resource names ("Control.GetAtespace" -> "Atespace")
// are real entries in resourceNames (internal/model/resources.go) - a
// made-up name would fail GroupByResource.
func fixtureAPI() *model.API {
	return &model.API{
		Services: []model.Service{
			{
				FullName: "test.Control",
				Name:     "Control",
				Comment:  "Svc does things.",
				Methods: []model.Method{
					{Name: "GetAtespace", Comment: "Do it.", InputName: "test.DoRequest", OutputName: "test.DoResponse", ServiceFullName: "test.Control", ServiceName: "Control"},
				},
			},
		},
		Messages: []model.Message{
			{
				FullName: "test.Atespace",
				Name:     "Atespace",
				Comment:  `Dangerous: <script>alert(1)</script> & "quotes"`,
				Fields: []model.Field{
					{Name: "scalar_field", TypeDisplay: "string"},
					{Name: "message_field", TypeDisplay: "DoRequest", TypeKind: "message", TypeFullName: "test.DoRequest"},
				},
			},
			{FullName: "test.DoRequest", Name: "DoRequest"},
			{FullName: "test.DoResponse", Name: "DoResponse"},
		},
		Enums: []model.Enum{
			{
				FullName: "test.Color",
				Name:     "Color",
				Values:   []model.EnumValue{{Name: "RED", Number: 1}},
			},
		},
	}
}

func TestRender_Anchors(t *testing.T) {
	out, err := docsite.Render(fixtureAPI())
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	html := string(out)

	for _, id := range []string{
		`id="resource-test.Atespace"`,
		`id="method-test.Control.GetAtespace"`,
		`id="msg-test.DoRequest"`,
		`id="msg-test.DoResponse"`,
		`id="enum-test.Color"`,
	} {
		if !strings.Contains(html, id) {
			t.Errorf("output missing anchor %s", id)
		}
	}
}

// TestRender_EscapesComments is a regression test for the search index's
// safety property: the dangerous comment text must never appear as a raw,
// executable <script> close inside the page - only inside the two
// legitimate embedded <script> blocks (the JSON index, and search.js), both
// of which are tool-authored, not proto-derived.
func TestRender_EscapesComments(t *testing.T) {
	out, err := docsite.Render(fixtureAPI())
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	html := string(out)

	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("output contains the dangerous comment text unescaped")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("output does not contain the comment's escaped form - comment may have been dropped")
	}
	if !strings.Contains(html, "&amp;") {
		t.Error("output does not escape '&' from the comment")
	}
	if got := strings.Count(html, "</script>"); got != 2 {
		t.Errorf("literal </script> count = %d, want 2 (the JSON index and search.js closes only)", got)
	}
}

func TestRender_SearchIndexRoundTrips(t *testing.T) {
	out, err := docsite.Render(fixtureAPI())
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	html := string(out)

	const openTag = `<script id="apitool-index" type="application/json">`
	start := strings.Index(html, openTag)
	if start < 0 {
		t.Fatal("search index <script> tag not found in output")
	}
	rest := html[start+len(openTag):]
	end := strings.Index(rest, "</script>")
	if end < 0 {
		t.Fatal("search index <script> tag not closed in output")
	}

	var entries []struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		FullName string `json:"fullName"`
		Anchor   string `json:"anchor"`
	}
	if err := json.Unmarshal([]byte(rest[:end]), &entries); err != nil {
		t.Fatalf("json.Unmarshal(search index) error = %v", err)
	}

	// Atespace resource group: 1 resource + 2 fields + 1 method = 4.
	// Other messages: DoRequest, DoResponse (no fields) = 2.
	// Enums: 1 enum + 1 enum value = 2.
	if want := 8; len(entries) != want {
		t.Fatalf("len(entries) = %d, want %d", len(entries), want)
	}

	want := []struct {
		Kind, Name, FullName, Anchor string
	}{
		{"resource", "Atespace", "test.Atespace", "resource-test.Atespace"},
		{"field", "scalar_field", "test.Atespace.scalar_field", "resource-test.Atespace"},
		{"method", "GetAtespace", "test.Control.GetAtespace", "method-test.Control.GetAtespace"},
		{"message", "DoRequest", "test.DoRequest", "msg-test.DoRequest"},
		{"enum value", "RED", "test.Color.RED", "enum-test.Color"},
	}
	for _, w := range want {
		found := false
		for _, e := range entries {
			if e.Kind == w.Kind && e.Name == w.Name && e.FullName == w.FullName && e.Anchor == w.Anchor {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("search index missing entry %+v", w)
		}
	}
}

// TestRender_RealAPISmoke exercises model.Build + docsite.Render against
// the real proto, filtered to Control first (same as cmd/generate.go) -
// other services' RPCs don't necessarily name a resource from
// resourceNames, so the unfiltered API would fail GroupByResource for
// unrelated reasons.
func TestRender_RealAPISmoke(t *testing.T) {
	fd, err := protoregistry.GlobalFiles.FindFileByPath("ateapi.proto")
	if err != nil {
		t.Fatalf("FindFileByPath(ateapi.proto) error = %v", err)
	}
	api := model.FilterToService(model.Build(fd), "Control")
	out, err := docsite.Render(api)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(string(out), "ateapi.Actor") {
		t.Error("output does not mention ateapi.Actor")
	}
}
