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
	"strings"

	"github.com/agent-substrate/substrate/tools/apitool/internal/model"
)

const createUpdateRequestEmbedsResourceName = "create-update-request-embeds-resource"

// CreateUpdateRequestEmbedsResource requires a Create/Update method's
// request to have a field whose type is the resource itself, named after
// the resource's own type name in snake_case (e.g. "actor_snapshot_tag"
// for ActorSnapshotTag, not "tag").
var CreateUpdateRequestEmbedsResource = Rule{
	Name: createUpdateRequestEmbedsResourceName,
	Description: "A Create/Update method's request has a field, named after the resource's " +
		"snake_case type name, whose type is the resource itself.",
	Check: checkCreateUpdateRequestEmbedsResource,
}

func checkCreateUpdateRequestEmbedsResource(api *model.API) []Finding {
	resources, err := model.GroupByResource(api)
	if err != nil {
		return []Finding{{Rule: createUpdateRequestEmbedsResourceName, Subject: "(GroupByResource)", Message: err.Error()}}
	}
	messagesByName := messagesByFullName(api)

	var findings []Finding
	for _, rg := range resources {
		for _, m := range rg.Methods {
			verb, ok := standardVerbFor(m.Name, rg.Message.Name)
			if !ok || (verb != "Create" && verb != "Update") {
				continue
			}
			subject := m.ServiceFullName + "." + m.Name

			req, ok := messagesByName[m.InputName]
			if !ok {
				findings = append(findings, Finding{Rule: createUpdateRequestEmbedsResourceName, Subject: subject, Message: "request type " + m.InputName + " not found"})
				continue
			}

			field := findFieldByType(req, rg.Message.FullName)
			if field == nil {
				findings = append(findings, Finding{
					Rule: createUpdateRequestEmbedsResourceName, Subject: subject,
					Message: fmt.Sprintf("request has no field of type %s", rg.Message.FullName),
				})
				continue
			}

			if want := snakeCase(rg.Message.Name); field.Name != want {
				findings = append(findings, Finding{
					Rule: createUpdateRequestEmbedsResourceName, Subject: subject,
					Message: fmt.Sprintf("resource field is named %q, want %q", field.Name, want),
				})
			}
		}
	}
	return findings
}

func findFieldByType(m model.Message, typeFullName string) *model.Field {
	for i := range m.Fields {
		if m.Fields[i].TypeKind == "message" && m.Fields[i].TypeFullName == typeFullName {
			return &m.Fields[i]
		}
	}
	return nil
}

// snakeCase converts PascalCase to lower_snake_case, e.g.
// "ActorSnapshotTag" -> "actor_snapshot_tag". Not acronym-aware.
func snakeCase(s string) string {
	return strings.ToLower(pascalBoundary.ReplaceAllString(s, "${1}_${2}"))
}
