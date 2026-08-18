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

const (
	standardMethodReturnsResourceName   = "standard-method-returns-resource"
	getDeleteRequestSingleObjectRefName = "get-delete-request-single-objectref"

	objectRefTypeFullName = "ateapi.ObjectRef"
)

// standardVerbs name a resource by simple concatenation: "{Verb}{Resource}"
// (e.g. "GetActor"). List isn't included - see list_methods.go.
var standardVerbs = []string{"Get", "Create", "Update", "Delete"}

// standardVerbFor reports the standard verb methodName uses to target
// resourceName, if any - e.g. ("Get", true) for ("GetActor", "Actor").
func standardVerbFor(methodName, resourceName string) (verb string, ok bool) {
	for _, v := range standardVerbs {
		if methodName == v+resourceName {
			return v, true
		}
	}
	return "", false
}

// StandardMethodReturnsResource requires a Get/Create/Update/Delete
// method's response to be the resource itself, never a wrapper message.
var StandardMethodReturnsResource = Rule{
	Name:        standardMethodReturnsResourceName,
	Description: "A Get/Create/Update/Delete method's response is the resource itself, not a wrapper message.",
	Check:       checkStandardMethodReturnsResource,
}

func checkStandardMethodReturnsResource(api *model.API) []Finding {
	resources, err := model.GroupByResource(api)
	if err != nil {
		return []Finding{{Rule: standardMethodReturnsResourceName, Subject: "(GroupByResource)", Message: err.Error()}}
	}

	var findings []Finding
	for _, rg := range resources {
		for _, m := range rg.Methods {
			verb, ok := standardVerbFor(m.Name, rg.Message.Name)
			if !ok {
				continue // a custom or List method - not this rule's concern.
			}
			if m.OutputName != rg.Message.FullName {
				findings = append(findings, Finding{
					Rule: standardMethodReturnsResourceName, Subject: m.ServiceFullName + "." + m.Name,
					Message: fmt.Sprintf("%s method returns %s, want the resource itself (%s)", verb, m.OutputName, rg.Message.FullName),
				})
			}
		}
	}
	return findings
}

// GetDeleteRequestSingleObjectRef requires a Get/Delete method's request to
// identify the resource with exactly one field, of type ObjectRef.
var GetDeleteRequestSingleObjectRef = Rule{
	Name:        getDeleteRequestSingleObjectRefName,
	Description: "A Get/Delete method's request has exactly one field, of type ObjectRef, identifying the resource.",
	Check:       checkGetDeleteRequestSingleObjectRef,
}

func checkGetDeleteRequestSingleObjectRef(api *model.API) []Finding {
	resources, err := model.GroupByResource(api)
	if err != nil {
		return []Finding{{Rule: getDeleteRequestSingleObjectRefName, Subject: "(GroupByResource)", Message: err.Error()}}
	}
	messagesByName := messagesByFullName(api)

	var findings []Finding
	for _, rg := range resources {
		for _, m := range rg.Methods {
			verb, ok := standardVerbFor(m.Name, rg.Message.Name)
			if !ok || (verb != "Get" && verb != "Delete") {
				continue
			}
			subject := m.ServiceFullName + "." + m.Name

			req, ok := messagesByName[m.InputName]
			if !ok {
				findings = append(findings, Finding{Rule: getDeleteRequestSingleObjectRefName, Subject: subject, Message: "request type " + m.InputName + " not found"})
				continue
			}
			if len(req.Fields) != 1 {
				findings = append(findings, Finding{
					Rule: getDeleteRequestSingleObjectRefName, Subject: subject,
					Message: fmt.Sprintf("request has %d field(s), want exactly 1 (an ObjectRef)", len(req.Fields)),
				})
				continue
			}
			if f := req.Fields[0]; f.TypeKind != "message" || f.TypeFullName != objectRefTypeFullName {
				findings = append(findings, Finding{
					Rule: getDeleteRequestSingleObjectRefName, Subject: subject,
					Message: fmt.Sprintf("request's only field is %s, want %s", fieldTypeDescription(f), objectRefTypeFullName),
				})
			}
		}
	}
	return findings
}
