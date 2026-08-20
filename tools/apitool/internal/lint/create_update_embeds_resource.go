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

	"github.com/agent-substrate/substrate/tools/apitool/internal/model"
)

// CreateUpdateRequestEmbedsResource requires a Create/Update method's
// request to have exactly one field whose type is the resource itself,
// named after the resource's own type name in snake_case (e.g.
// "actor_snapshot_tag" for ActorSnapshotTag, not "tag").
var CreateUpdateRequestEmbedsResource = Rule{
	Name: "create-update-request-embeds-resource",
	Description: "A Create/Update method's request has exactly one field, named after the " +
		"resource's snake_case type name, whose type is the resource itself.",
	Check: checkCreateUpdateRequestEmbedsResource,
}

func checkCreateUpdateRequestEmbedsResource(api *model.API) ([]Finding, error) {
	resources, err := model.Resources(api)
	if err != nil {
		return nil, err
	}
	messagesByName := api.MessagesByFullName()

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
				findings = append(findings, Finding{Subject: subject, Message: "request type " + m.InputName + " not found"})
				continue
			}

			fields := req.FieldsByType(rg.Message.FullName)
			if len(fields) != 1 {
				findings = append(findings, Finding{
					Subject: subject,
					Message: fmt.Sprintf("request has %d field(s) of type %s, want exactly 1", len(fields), rg.Message.FullName),
				})
				continue
			}

			if want := fieldNameForResource(rg.Message.Name); fields[0].Name != want {
				findings = append(findings, Finding{
					Subject: subject,
					Message: fmt.Sprintf("resource field is named %q, want %q", fields[0].Name, want),
				})
			}
		}
	}
	return findings, nil
}
