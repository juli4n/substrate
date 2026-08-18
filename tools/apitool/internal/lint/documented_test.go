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

func TestDocumented(t *testing.T) {
	tests := []struct {
		name          string
		fieldComment  string
		methodComment string
		wantFindings  int
	}{
		{"both documented", "does a thing", "does another thing", 0},
		{"field undocumented", "", "does another thing", 1},
		{"method undocumented", "does a thing", "", 1},
		{"neither documented", "", "", 2},
		{"whitespace-only comment counts as undocumented", "   ", "\n", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &model.API{
				Services: []model.Service{{
					Name: "Control",
					Methods: []model.Method{
						{Name: "DoThing", ServiceName: "Control", Comment: tt.methodComment, InputName: "test.DoThingRequest", OutputName: "test.DoThingResponse"},
					},
				}},
				Messages: []model.Message{
					{FullName: "test.DoThingRequest", Name: "DoThingRequest", Fields: []model.Field{
						{Name: "widget", Comment: tt.fieldComment},
					}},
				},
			}
			findings := lint.Documented.Check(api)
			if len(findings) != tt.wantFindings {
				t.Errorf("Check() = %+v, want %d finding(s)", findings, tt.wantFindings)
			}
		})
	}
}
