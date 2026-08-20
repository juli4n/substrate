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
	objectRefTypeFullName     = "ateapi.ObjectRef"
	deleteOptionsTypeFullName = "ateapi.DeleteOptions"
)

// StandardMethodReturnsResource requires a Get/Create/Update/Delete
// method's response to be the resource itself, never a wrapper message.
var StandardMethodReturnsResource = Rule{
	Name:        "standard-method-returns-resource",
	Description: "A Get/Create/Update/Delete method's response is the resource itself, not a wrapper message.",
	Check:       checkStandardMethodReturnsResource,
}

func checkStandardMethodReturnsResource(api *model.API) ([]Finding, error) {
	resources, err := model.Resources(api)
	if err != nil {
		return nil, err
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
					Subject: m.ServiceFullName + "." + m.Name,
					Message: fmt.Sprintf("%s method returns %s, want the resource itself (%s)", verb, m.OutputName, rg.Message.FullName),
				})
			}
		}
	}
	return findings, nil
}

// GetRequestSingleObjectRef requires a Get method's request to identify the
// resource with exactly one field, of type ObjectRef and named after the
// resource's snake_case type name.
var GetRequestSingleObjectRef = Rule{
	Name:        "get-request-single-objectref",
	Description: "A Get method's request has exactly one field, of type ObjectRef and named after the resource's snake_case type name, identifying the resource.",
	Check:       checkGetRequestSingleObjectRef,
}

func checkGetRequestSingleObjectRef(api *model.API) ([]Finding, error) {
	resources, err := model.Resources(api)
	if err != nil {
		return nil, err
	}
	messagesByName := api.MessagesByFullName()

	var findings []Finding
	for _, rg := range resources {
		for _, m := range rg.Methods {
			verb, ok := standardVerbFor(m.Name, rg.Message.Name)
			if !ok || verb != "Get" {
				continue
			}
			subject := m.ServiceFullName + "." + m.Name

			req, ok := messagesByName[m.InputName]
			if !ok {
				findings = append(findings, Finding{Subject: subject, Message: "request type " + m.InputName + " not found"})
				continue
			}
			if len(req.Fields) != 1 {
				findings = append(findings, Finding{
					Subject: subject,
					Message: fmt.Sprintf("request has %d field(s), want exactly 1 (an ObjectRef)", len(req.Fields)),
				})
				continue
			}
			f := req.Fields[0]
			if f.TypeKind != "message" || f.TypeFullName != objectRefTypeFullName {
				findings = append(findings, Finding{
					Subject: subject,
					Message: fmt.Sprintf("request's only field is %s, want %s", fieldTypeDescription(f), objectRefTypeFullName),
				})
				continue
			}
			if want := fieldNameForResource(rg.Message.Name); f.Name != want {
				findings = append(findings, Finding{
					Subject: subject,
					Message: fmt.Sprintf("resource field is named %q, want %q", f.Name, want),
				})
			}
		}
	}
	return findings, nil
}

// DeleteRequestShape requires a Delete method's request to identify the
// resource with exactly one ObjectRef field, named after the resource's
// snake_case type name, plus an optional DeleteOptions field for
// preconditions.
var DeleteRequestShape = Rule{
	Name:        "delete-request-shape",
	Description: "A Delete method's request has exactly one ObjectRef field, named after the resource's snake_case type name, plus an optional DeleteOptions field - nothing else.",
	Check:       checkDeleteRequestShape,
}

func checkDeleteRequestShape(api *model.API) ([]Finding, error) {
	resources, err := model.Resources(api)
	if err != nil {
		return nil, err
	}
	messagesByName := api.MessagesByFullName()

	var findings []Finding
	for _, rg := range resources {
		for _, m := range rg.Methods {
			verb, ok := standardVerbFor(m.Name, rg.Message.Name)
			if !ok || verb != "Delete" {
				continue
			}
			subject := m.ServiceFullName + "." + m.Name

			req, ok := messagesByName[m.InputName]
			if !ok {
				findings = append(findings, Finding{Subject: subject, Message: "request type " + m.InputName + " not found"})
				continue
			}

			var objectRefFields, optionsFields int
			for _, f := range req.Fields {
				switch {
				case f.TypeKind == "message" && f.TypeFullName == objectRefTypeFullName:
					objectRefFields++
					if want := fieldNameForResource(rg.Message.Name); f.Name != want {
						findings = append(findings, Finding{
							Subject: subject,
							Message: fmt.Sprintf("resource field is named %q, want %q", f.Name, want),
						})
					}
				case f.TypeKind == "message" && f.TypeFullName == deleteOptionsTypeFullName:
					optionsFields++
				default:
					findings = append(findings, Finding{
						Subject: subject,
						Message: fmt.Sprintf("field %q is %s, want %s or %s - non-resource control fields belong in %s", f.Name, fieldTypeDescription(f), objectRefTypeFullName, deleteOptionsTypeFullName, deleteOptionsTypeFullName),
					})
				}
			}
			if objectRefFields != 1 {
				findings = append(findings, Finding{
					Subject: subject,
					Message: fmt.Sprintf("request has %d field(s) of type %s, want exactly 1 identifying the resource", objectRefFields, objectRefTypeFullName),
				})
			}
			if optionsFields > 1 {
				findings = append(findings, Finding{
					Subject: subject,
					Message: fmt.Sprintf("request has %d field(s) of type %s, want at most 1", optionsFields, deleteOptionsTypeFullName),
				})
			}
		}
	}
	return findings, nil
}

func standardVerbFor(methodName, resourceName string) (verb string, ok bool) {
	for _, v := range []string{"Get", "Create", "Update", "Delete"} {
		if methodName == v+resourceName {
			return v, true
		}
	}
	return "", false
}
