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

package lint_test

import (
	"testing"

	"github.com/agent-substrate/substrate/tools/apitool/internal/lint"
	"github.com/agent-substrate/substrate/tools/apitool/internal/model"
)

// standardMethodAPI builds a minimal API with one Control.GetAtespace
// method (see resourceAPI's comment) whose response is outputName,
// targeting an "Atespace" resource with the given fields.
func standardMethodAPI(outputName string, atespaceFields []model.Field) *model.API {
	return &model.API{
		Services: []model.Service{{
			Name: "Control",
			Methods: []model.Method{
				{Name: "GetAtespace", ServiceName: "Control", InputName: "test.GetAtespaceRequest", OutputName: outputName},
			},
		}},
		Messages: []model.Message{
			{FullName: "test.Atespace", Name: "Atespace", Fields: atespaceFields},
			{FullName: "test.GetAtespaceRequest", Name: "GetAtespaceRequest", Fields: []model.Field{
				{Name: "atespace", TypeKind: "message", TypeFullName: "ateapi.ObjectRef"},
			}},
			{FullName: "test.GetAtespaceResponse", Name: "GetAtespaceResponse"},
		},
	}
}

func TestStandardMethodReturnsResource(t *testing.T) {
	tests := []struct {
		name        string
		outputName  string
		wantFinding bool
	}{
		{"returns the resource directly", "test.Atespace", false},
		{"returns a wrapper instead", "test.GetAtespaceResponse", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := lint.StandardMethodReturnsResource.Check(standardMethodAPI(tt.outputName, nil))
			if got := len(findings) > 0; got != tt.wantFinding {
				t.Errorf("Check() findings = %+v, want a finding: %v", findings, tt.wantFinding)
			}
		})
	}
}

func TestGetDeleteRequestSingleObjectRef(t *testing.T) {
	objectRefField := model.Field{Name: "atespace", TypeKind: "message", TypeFullName: "ateapi.ObjectRef"}

	tests := []struct {
		name        string
		reqFields   []model.Field
		wantFinding bool
	}{
		{"single ObjectRef field", []model.Field{objectRefField}, false},
		{"no fields", nil, true},
		{"two fields", []model.Field{objectRefField, {Name: "extra"}}, true},
		{"single field, wrong type", []model.Field{{Name: "name"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := standardMethodAPI("test.Atespace", nil)
			api.Messages[1].Fields = tt.reqFields // test.GetAtespaceRequest
			findings := lint.GetDeleteRequestSingleObjectRef.Check(api)
			if got := len(findings) > 0; got != tt.wantFinding {
				t.Errorf("Check() findings = %+v, want a finding: %v", findings, tt.wantFinding)
			}
		})
	}
}

// TestGetDeleteRequestSingleObjectRef_IgnoresCreateUpdate confirms the rule
// ignores Create/Update, which legitimately embed the full resource.
func TestGetDeleteRequestSingleObjectRef_IgnoresCreateUpdate(t *testing.T) {
	api := &model.API{
		Services: []model.Service{{
			Name: "Control",
			Methods: []model.Method{
				{Name: "CreateAtespace", ServiceName: "Control", InputName: "test.CreateAtespaceRequest", OutputName: "test.Atespace"},
			},
		}},
		Messages: []model.Message{
			{FullName: "test.Atespace", Name: "Atespace"},
			{FullName: "test.CreateAtespaceRequest", Name: "CreateAtespaceRequest", Fields: []model.Field{
				{Name: "atespace", TypeKind: "message", TypeFullName: "test.Atespace"}, // embeds the resource, not an ObjectRef
			}},
		},
	}
	if findings := lint.GetDeleteRequestSingleObjectRef.Check(api); len(findings) != 0 {
		t.Errorf("Check() = %+v, want no findings for a Create method", findings)
	}
}
