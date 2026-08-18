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

package lint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agent-substrate/substrate/tools/apitool/internal/model"
)

const (
	enumZeroValueUnspecifiedName = "enum-zero-value-unspecified"
	enumValuesPrefixedName       = "enum-values-prefixed"
)

// EnumZeroValueUnspecified requires every enum's zero value to be named
// "{EnumName}_UNSPECIFIED". EnumName is the enum's own short name - for a
// nested enum (e.g. ExternalVolume.Status) that's "Status", not the
// parent's name.
var EnumZeroValueUnspecified = Rule{
	Name:        enumZeroValueUnspecifiedName,
	Description: `Every enum's zero value is named "{EnumName}_UNSPECIFIED".`,
	Check:       checkEnumZeroValueUnspecified,
}

// EnumValuesPrefixed requires every top-level enum's values to be prefixed
// with the enum's own name. Nested enum values are exempt - the style
// guide requires they NOT be prefixed.
var EnumValuesPrefixed = Rule{
	Name:        enumValuesPrefixedName,
	Description: `Every top-level enum's values are prefixed with "{ENUM_NAME}_" (nested enums are exempt).`,
	Check:       checkEnumValuesPrefixed,
}

func checkEnumZeroValueUnspecified(api *model.API) []Finding {
	var findings []Finding
	for _, e := range api.Enums {
		want := upperSnake(lastNameSegment(e.Name)) + "_UNSPECIFIED"
		zero := findEnumValueByNumber(e, 0)
		switch {
		case zero == nil:
			findings = append(findings, Finding{Rule: enumZeroValueUnspecifiedName, Subject: e.FullName, Message: "has no value numbered 0"})
		case zero.Name != want:
			findings = append(findings, Finding{
				Rule: enumZeroValueUnspecifiedName, Subject: e.FullName,
				Message: fmt.Sprintf("zero value is named %q, want %q", zero.Name, want),
			})
		}
	}
	return findings
}

func checkEnumValuesPrefixed(api *model.API) []Finding {
	var findings []Finding
	for _, e := range api.Enums {
		if e.ParentFullName != "" {
			continue // nested: values must NOT be prefixed, not this rule's concern.
		}
		prefix := upperSnake(e.Name) + "_"
		for _, v := range e.Values {
			if !strings.HasPrefix(v.Name, prefix) {
				findings = append(findings, Finding{
					Rule: enumValuesPrefixedName, Subject: e.FullName + "." + v.Name,
					Message: fmt.Sprintf("not prefixed with %q", prefix),
				})
			}
		}
	}
	return findings
}

func findEnumValueByNumber(e model.Enum, number int32) *model.EnumValue {
	for i := range e.Values {
		if e.Values[i].Number == number {
			return &e.Values[i]
		}
	}
	return nil
}

// lastNameSegment returns the part of a name after its last ".", e.g.
// "Actor.Status" -> "Status".
func lastNameSegment(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

var pascalBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// upperSnake converts PascalCase to UPPER_SNAKE_CASE, e.g. "ActorState" ->
// "ACTOR_STATE". Not acronym-aware: "HTTPStatus" would become "HTTPSTATUS".
func upperSnake(s string) string {
	return strings.ToUpper(pascalBoundary.ReplaceAllString(s, "${1}_${2}"))
}
