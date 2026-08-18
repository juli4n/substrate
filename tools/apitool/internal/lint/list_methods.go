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

const (
	listMethodShapeName               = "list-method-shape"
	listResponseNameMatchesMethodName = "list-response-name-matches-method"
)

// ListMethodShape requires a "List*" method's request to have
// page_size/page_token fields, and its response to have a repeated message
// field plus next_page_token.
var ListMethodShape = Rule{
	Name: listMethodShapeName,
	Description: `A "List*" method's request has page_size/page_token fields, and its ` +
		"response has a repeated message field plus a next_page_token field.",
	Check: checkListMethodShape,
}

// ListResponseNameMatchesMethod requires a "List*" method's response to be
// named "{MethodName}Response" - unlike Get/Create/Update/Delete, which
// return the resource directly (see StandardMethodReturnsResource).
var ListResponseNameMatchesMethod = Rule{
	Name:        listResponseNameMatchesMethodName,
	Description: `Every "List*" method's response message is named "{MethodName}Response".`,
	Check:       checkListResponseNameMatchesMethod,
}

func checkListMethodShape(api *model.API) []Finding {
	messagesByName := messagesByFullName(api)

	var findings []Finding
	for _, svc := range api.Services {
		for _, method := range svc.Methods {
			if !strings.HasPrefix(method.Name, "List") {
				continue
			}
			subject := method.ServiceFullName + "." + method.Name
			findings = append(findings, checkListRequest(messagesByName, subject, method.InputName)...)
			findings = append(findings, checkListResponse(messagesByName, subject, method.OutputName)...)
		}
	}
	return findings
}

func checkListRequest(messagesByName map[string]model.Message, subject, inputName string) []Finding {
	req, ok := messagesByName[inputName]
	if !ok {
		return []Finding{{Rule: listMethodShapeName, Subject: subject, Message: "request type " + inputName + " not found"}}
	}

	var findings []Finding
	if findFieldByName(req, "page_size") == nil {
		findings = append(findings, Finding{Rule: listMethodShapeName, Subject: subject, Message: "request has no page_size field"})
	}
	if findFieldByName(req, "page_token") == nil {
		findings = append(findings, Finding{Rule: listMethodShapeName, Subject: subject, Message: "request has no page_token field"})
	}
	return findings
}

func checkListResponse(messagesByName map[string]model.Message, subject, outputName string) []Finding {
	resp, ok := messagesByName[outputName]
	if !ok {
		return []Finding{{Rule: listMethodShapeName, Subject: subject, Message: "response type " + outputName + " not found"}}
	}

	var findings []Finding
	if !hasRepeatedMessageField(resp) {
		findings = append(findings, Finding{Rule: listMethodShapeName, Subject: subject, Message: "response has no repeated message field for the page of results"})
	}
	if findFieldByName(resp, "next_page_token") == nil {
		findings = append(findings, Finding{Rule: listMethodShapeName, Subject: subject, Message: "response has no next_page_token field"})
	}
	return findings
}

func hasRepeatedMessageField(m model.Message) bool {
	for _, f := range m.Fields {
		if f.Repeated && f.TypeKind == "message" {
			return true
		}
	}
	return false
}

func checkListResponseNameMatchesMethod(api *model.API) []Finding {
	messagesByName := messagesByFullName(api)

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
				findings = append(findings, Finding{Rule: listResponseNameMatchesMethodName, Subject: subject, Message: "response type " + method.OutputName + " not found"})
				continue
			}
			if resp.Name != want {
				findings = append(findings, Finding{
					Rule: listResponseNameMatchesMethodName, Subject: subject,
					Message: fmt.Sprintf("response message is named %q, want %q", resp.Name, want),
				})
			}
		}
	}
	return findings
}
