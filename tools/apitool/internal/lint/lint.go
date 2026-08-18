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

// Package lint runs a set of predicates ("rules") against a model.API,
// catching API-shape mistakes.
package lint

import "github.com/agent-substrate/substrate/tools/apitool/internal/model"

// Finding is one rule violation: which rule found it, which API entity it's
// about (e.g. "ateapi.Worker", "Control.ListActors"), and why.
type Finding struct {
	Rule    string
	Subject string
	Message string
}

// Rule is one predicate over the API model. Check returns one Finding per
// violation, or nil if api satisfies the rule.
type Rule struct {
	Name        string
	Description string
	Check       func(api *model.API) []Finding
}

// Run executes every rule against api and returns every finding.
func Run(api *model.API, rules []Rule) []Finding {
	var findings []Finding
	for _, r := range rules {
		findings = append(findings, r.Check(api)...)
	}
	return findings
}

// messagesByFullName indexes api.Messages for looking up a Method's
// InputName/OutputName.
func messagesByFullName(api *model.API) map[string]model.Message {
	byName := make(map[string]model.Message, len(api.Messages))
	for _, m := range api.Messages {
		byName[m.FullName] = m
	}
	return byName
}
