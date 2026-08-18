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

package model

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed examples/*.json
var exampleFS embed.FS

// exampleJSON holds one hand-authored example value per message docsite can
// show one for - a resource's own message, plus every RPC's request/
// response type (see TestExampleJSON_CoversRenderedMessages) - keyed by
// full name (e.g. "ateapi.Actor"), in protojson form. Each entry is one
// file under examples/, named "<full name>.json", pretty-printed exactly as
// it should render.
//
// Hand-written, not derived from the schema, so it can drift -
// examples_test.go checks each example's JSON keys against the real
// fields.
var exampleJSON = loadExampleJSON()

func loadExampleJSON() map[string]string {
	entries, err := exampleFS.ReadDir("examples")
	if err != nil {
		panic(fmt.Sprintf("model: reading embedded examples directory: %v", err))
	}

	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		fullName, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok {
			continue // not one of ours: embed's glob already restricts this, but skip rather than assume.
		}
		data, err := exampleFS.ReadFile("examples/" + entry.Name())
		if err != nil {
			panic(fmt.Sprintf("model: reading embedded example %q: %v", entry.Name(), err))
		}
		out[fullName] = strings.TrimRight(string(data), "\n")
	}
	return out
}
