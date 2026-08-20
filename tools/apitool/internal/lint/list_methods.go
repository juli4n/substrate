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

// ListMethodShape requires a "List*" method's request to have
// page_size/page_token fields, and its response to have a repeated message
// field.
var ListMethodShape = Rule{
	Name: "list-method-shape",
	Description: `A "List*" method's request has page_size/page_token fields, and its ` +
		"response has a repeated message field, named after the method's own plural, plus a next_page_token field.",
	Check: checkListMethodShape,
}

// ListResponseNameMatchesMethod requires a "List*" method's response to be
// named "{MethodName}Response".
var ListResponseNameMatchesMethod = Rule{
	Name:        "list-response-name-matches-method",
	Description: `Every "List*" method's response message is named "{MethodName}Response".`,
	Check:       checkListResponseNameMatchesMethod,
}

func checkListMethodShape(api *model.API) ([]Finding, error) {
	messagesByName := api.MessagesByFullName()

	var findings []Finding
	for _, svc := range api.Services {
		for _, method := range svc.Methods {
			if !strings.HasPrefix(method.Name, "List") {
				continue
			}
			subject := method.ServiceFullName + "." + method.Name
			findings = append(findings, checkListRequest(messagesByName, subject, method.InputName)...)
			findings = append(findings, checkListResponse(messagesByName, subject, method.Name, method.OutputName)...)
		}
	}
	return findings, nil
}

func checkListRequest(messagesByName map[string]model.Message, subject, inputName string) []Finding {
	req, ok := messagesByName[inputName]
	if !ok {
		return []Finding{{Subject: subject, Message: "request type " + inputName + " not found"}}
	}

	var findings []Finding
	if findFieldByName(req, "page_size") == nil {
		findings = append(findings, Finding{Subject: subject, Message: "request has no page_size field"})
	}
	if findFieldByName(req, "page_token") == nil {
		findings = append(findings, Finding{Subject: subject, Message: "request has no page_token field"})
	}
	return findings
}

func checkListResponse(messagesByName map[string]model.Message, subject, methodName, outputName string) []Finding {
	resp, ok := messagesByName[outputName]
	if !ok {
		return []Finding{{Subject: subject, Message: "response type " + outputName + " not found"}}
	}

	var findings []Finding
	if field := findRepeatedMessageField(resp); field == nil {
		findings = append(findings, Finding{Subject: subject, Message: "response has no repeated message field for the page of results"})
	} else if want := repeatedFieldNameForList(methodName); field.Name != want {
		findings = append(findings, Finding{
			Subject: subject,
			Message: fmt.Sprintf("repeated field is named %q, want %q (the plural resource name, matching the method name)", field.Name, want),
		})
	}
	if findFieldByName(resp, "next_page_token") == nil {
		findings = append(findings, Finding{Subject: subject, Message: "response has no next_page_token field"})
	}
	return findings
}

func findRepeatedMessageField(m model.Message) *model.Field {
	for i := range m.Fields {
		if m.Fields[i].Repeated && m.Fields[i].TypeKind == "message" {
			return &m.Fields[i]
		}
	}
	return nil
}

func checkListResponseNameMatchesMethod(api *model.API) ([]Finding, error) {
	messagesByName := api.MessagesByFullName()

	var findings []Finding
	for _, svc := range api.Services {
		for _, method := range svc.Methods {
			if !strings.HasPrefix(method.Name, "List") {
				continue
			}
			subject := method.ServiceFullName + "." + method.Name
			want := method.Name + "Response"

			resp, ok := messagesByName[method.OutputName]
			if !ok {
				findings = append(findings, Finding{Subject: subject, Message: "response type " + method.OutputName + " not found"})
				continue
			}
			if resp.Name != want {
				findings = append(findings, Finding{
					Subject: subject,
					Message: fmt.Sprintf("response message is named %q, want %q", resp.Name, want),
				})
			}
		}
	}
	return findings, nil
}
