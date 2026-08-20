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

// createUpdateAPI builds a minimal API with one Control.CreateAtespace
// method (see resourceAPI's comment) whose request has the given fields.
func createUpdateAPI(reqFields []model.Field) *model.API {
	return &model.API{
		Services: []model.Service{{
			Name: "Control",
			Methods: []model.Method{
				{Name: "CreateAtespace", ServiceName: "Control", InputName: "test.CreateAtespaceRequest", OutputName: "test.Atespace"},
			},
		}},
		Messages: []model.Message{
			{FullName: "test.Atespace", Name: "Atespace"},
			{FullName: "test.CreateAtespaceRequest", Name: "CreateAtespaceRequest", Fields: reqFields},
		},
	}
}

func TestCreateUpdateRequestEmbedsResource(t *testing.T) {
	resourceField := model.Field{Name: "atespace", TypeKind: "message", TypeFullName: "test.Atespace"}

	tests := []struct {
		name        string
		reqFields   []model.Field
		wantFinding bool
	}{
		{"correctly named resource field", []model.Field{resourceField}, false},
		{"no resource-typed field at all", []model.Field{{Name: "name"}}, true},
		{
			name:        "resource field present but misnamed",
			reqFields:   []model.Field{{Name: "space", TypeKind: "message", TypeFullName: "test.Atespace"}},
			wantFinding: true,
		},
		{
			name: "two resource-typed fields",
			reqFields: []model.Field{
				resourceField,
				{Name: "previous", TypeKind: "message", TypeFullName: "test.Atespace"},
			},
			wantFinding: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, err := lint.CreateUpdateRequestEmbedsResource.Check(createUpdateAPI(tt.reqFields))
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if got := len(findings) > 0; got != tt.wantFinding {
				t.Errorf("Check() findings = %+v, want a finding: %v", findings, tt.wantFinding)
			}
		})
	}
}

// TestCreateUpdateRequestEmbedsResource_IgnoresGetDelete confirms the rule
// ignores Get/Delete, which reference the resource via ObjectRef instead.
func TestCreateUpdateRequestEmbedsResource_IgnoresGetDelete(t *testing.T) {
	api := &model.API{
		Services: []model.Service{{
			Name: "Control",
			Methods: []model.Method{
				{Name: "GetAtespace", ServiceName: "Control", InputName: "test.GetAtespaceRequest", OutputName: "test.Atespace"},
			},
		}},
		Messages: []model.Message{
			{FullName: "test.Atespace", Name: "Atespace"},
			{FullName: "test.GetAtespaceRequest", Name: "GetAtespaceRequest", Fields: []model.Field{
				{Name: "atespace", TypeKind: "message", TypeFullName: "ateapi.ObjectRef"},
			}},
		},
	}
	findings, err := lint.CreateUpdateRequestEmbedsResource.Check(api)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("Check() = %+v, want no findings for a Get method", findings)
	}
}
