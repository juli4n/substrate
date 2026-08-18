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

// Package docsite renders a model.API into a single self-contained HTML
// page: no external stylesheets, scripts, fonts, or network requests.
package docsite

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"

	"github.com/agent-substrate/substrate/tools/apitool/internal/model"
)

//go:embed template.html.tmpl
var pageTemplate string

//go:embed style.css
var pageStyle string

//go:embed search.js
var pageScript string

// indexEntry is one row of the client-side search index. Marshaled with
// encoding/json's default HTML-escaping (do not disable it) - that's what
// keeps a literal "</script>" in a doc comment from closing the <script>
// tag it's embedded in.
type indexEntry struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	FullName string `json:"fullName"`
	Anchor   string `json:"anchor"`
}

type pageData struct {
	API           *model.API
	Resources     []resourceView  // one per model.ResourceGroup, each with its own attribute tree and methods
	OtherMessages []model.Message // API.Messages minus the resource messages, which get their own Resources view instead
	Style         template.CSS    // tool-authored, verbatim
	Script        template.JS     // tool-authored, verbatim
	Index         template.JS     // proto-derived, but already JSON-escaped by buildIndex
}

// resourceView is a model.ResourceGroup with the template's data
// pre-resolved: the object's own attribute tree, plus each method's
// params/response trees and examples.
type resourceView struct {
	Message model.Message
	Attrs   []model.AttrNode
	Methods []methodView
}

type methodView struct {
	model.Method
	Params          []model.AttrNode // built from the request message's own fields
	Response        []model.AttrNode // built from the response message's own fields
	RequestExample  string           // request message's Example ("" if it has none)
	ResponseExample string           // response message's Example ("" if it has none)
}

// Render produces the complete HTML document for api.
func Render(api *model.API) ([]byte, error) {
	sorted := sortedAPI(api)

	resources, err := model.GroupByResource(sorted)
	if err != nil {
		return nil, fmt.Errorf("while grouping methods by resource: %w", err)
	}

	resourceNames := make(map[string]bool, len(resources))
	for _, rg := range resources {
		resourceNames[rg.Message.Name] = true
	}
	otherMessages := make([]model.Message, 0, len(sorted.Messages))
	for _, m := range sorted.Messages {
		if !resourceNames[m.Name] {
			otherMessages = append(otherMessages, m)
		}
	}

	index, err := buildIndex(sorted, resources)
	if err != nil {
		return nil, fmt.Errorf("while building search index: %w", err)
	}

	tmpl, err := template.New("apitool").Funcs(template.FuncMap{
		"methodAnchor":   methodAnchor,
		"msgAnchor":      msgAnchor,
		"enumAnchor":     enumAnchor,
		"resourceAnchor": resourceAnchor,
		"highlightJSON":  highlightJSON,
		"nextAttrID":     newAttrIDFunc(),
		"markdown":       markdown,
	}).Parse(pageTemplate)
	if err != nil {
		return nil, fmt.Errorf("while parsing template: %w", err)
	}

	var buf bytes.Buffer
	data := pageData{
		API:           sorted,
		Resources:     buildResourceViews(sorted, resources),
		OtherMessages: otherMessages,
		Style:         template.CSS(pageStyle),
		Script:        template.JS(pageScript),
		Index:         template.JS(index),
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("while executing template: %w", err)
	}
	return buf.Bytes(), nil
}

func buildResourceViews(api *model.API, resources []model.ResourceGroup) []resourceView {
	messagesByName := make(map[string]model.Message, len(api.Messages))
	for _, m := range api.Messages {
		messagesByName[m.FullName] = m
	}

	views := make([]resourceView, 0, len(resources))
	for _, rg := range resources {
		rv := resourceView{
			Message: rg.Message,
			Attrs:   model.BuildAttrTree(api, rg.Message.Fields),
		}
		for _, m := range rg.Methods {
			mv := methodView{Method: m}
			if req, ok := messagesByName[m.InputName]; ok {
				mv.Params = model.BuildAttrTree(api, req.Fields)
				mv.RequestExample = req.Example
			}
			if resp, ok := messagesByName[m.OutputName]; ok {
				mv.Response = model.BuildAttrTree(api, resp.Fields)
				mv.ResponseExample = resp.Example
			}
			rv.Methods = append(rv.Methods, mv)
		}
		views = append(views, rv)
	}
	return views
}

// sortedAPI returns a copy of api with Services/Messages/Enums sorted by
// display name. Methods, fields, and enum values keep declaration order.
func sortedAPI(api *model.API) *model.API {
	out := &model.API{
		Services: append([]model.Service(nil), api.Services...),
		Messages: append([]model.Message(nil), api.Messages...),
		Enums:    append([]model.Enum(nil), api.Enums...),
	}
	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Name < out.Services[j].Name })
	sort.Slice(out.Messages, func(i, j int) bool { return out.Messages[i].Name < out.Messages[j].Name })
	sort.Slice(out.Enums, func(i, j int) bool { return out.Enums[i].Name < out.Enums[j].Name })
	return out
}

func buildIndex(api *model.API, resources []model.ResourceGroup) ([]byte, error) {
	resourceNames := make(map[string]bool, len(resources))

	var entries []indexEntry
	for _, rg := range resources {
		resourceNames[rg.Message.Name] = true
		entries = append(entries, indexEntry{Kind: "resource", Name: rg.Message.Name, FullName: rg.Message.FullName, Anchor: resourceAnchor(rg.Message.FullName)})
		for _, f := range rg.Message.Fields {
			entries = append(entries, indexEntry{Kind: "field", Name: f.Name, FullName: rg.Message.FullName + "." + f.Name, Anchor: resourceAnchor(rg.Message.FullName)})
		}
		for _, m := range rg.Methods {
			entries = append(entries, indexEntry{Kind: "method", Name: m.Name, FullName: m.ServiceFullName + "." + m.Name, Anchor: methodAnchor(m.ServiceFullName, m.Name)})
		}
	}
	for _, msg := range api.Messages {
		if resourceNames[msg.Name] {
			continue // already indexed above, pointing at its resource anchor instead
		}
		entries = append(entries, indexEntry{Kind: "message", Name: msg.Name, FullName: msg.FullName, Anchor: msgAnchor(msg.FullName)})
		for _, f := range msg.Fields {
			entries = append(entries, indexEntry{Kind: "field", Name: f.Name, FullName: msg.FullName + "." + f.Name, Anchor: msgAnchor(msg.FullName)})
		}
	}
	for _, e := range api.Enums {
		entries = append(entries, indexEntry{Kind: "enum", Name: e.Name, FullName: e.FullName, Anchor: enumAnchor(e.FullName)})
		for _, v := range e.Values {
			entries = append(entries, indexEntry{Kind: "enum value", Name: v.Name, FullName: e.FullName + "." + v.Name, Anchor: enumAnchor(e.FullName)})
		}
	}
	// encoding/json's default HTML-escaping is load-bearing here - see
	// indexEntry's doc comment.
	return json.Marshal(entries)
}

func methodAnchor(svcFullName, name string) string { return "method-" + svcFullName + "." + name }
func msgAnchor(fullName string) string             { return "msg-" + fullName }
func enumAnchor(fullName string) string            { return "enum-" + fullName }
func resourceAnchor(fullName string) string        { return "resource-" + fullName }

// newAttrIDFunc mints a fresh, page-unique id per call - a name-derived id
// would collide across attributes sharing a field name (e.g. "metadata").
func newAttrIDFunc() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("attr-children-%d", n)
	}
}
