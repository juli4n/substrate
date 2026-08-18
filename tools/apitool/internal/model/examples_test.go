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

package model

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/agent-substrate/substrate/pkg/proto/ateapipb"
)

// TestExampleJSON_MatchesSchema catches an example that has drifted from the
// proto it illustrates: a key that doesn't name a real field (typo, renamed,
// or removed field), or a value shaped wrong for its field (e.g. a JSON
// object where the field is a scalar). It does not require every field of a
// message to appear - examples are illustrative, not exhaustive - and it
// does not validate a scalar's content (e.g. that a string "looks like" a
// UUID), only its JSON shape against the schema. Exercises the same
// validateExample used by ValidateExampleJSON (and so by `apitool
// generate`), against every message in ateapi.proto - not just Control's -
// since an example could in principle illustrate any message.
func TestExampleJSON_MatchesSchema(t *testing.T) {
	fd, err := protoregistry.GlobalFiles.FindFileByPath("ateapi.proto")
	if err != nil {
		t.Fatalf("FindFileByPath(ateapi.proto) error = %v", err)
	}
	api := Build(fd)
	messagesByName := make(map[string]*Message, len(api.Messages))
	for i := range api.Messages {
		messagesByName[api.Messages[i].FullName] = &api.Messages[i]
	}

	for fullName, raw := range exampleJSON {
		t.Run(fullName, func(t *testing.T) {
			if err := validateExample(messagesByName, fullName, raw); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestExampleJSON_CoversRenderedMessages requires every message docsite can
// show an example for - a resource's own message, and every RPC's
// request/response type - to have one. Deliberately narrower than "every
// message reachable from Control": docsite never renders .Example for
// anything else.
func TestExampleJSON_CoversRenderedMessages(t *testing.T) {
	fd, err := protoregistry.GlobalFiles.FindFileByPath("ateapi.proto")
	if err != nil {
		t.Fatalf("FindFileByPath(ateapi.proto) error = %v", err)
	}
	api := FilterToService(Build(fd), "Control")

	resources, err := GroupByResource(api)
	if err != nil {
		t.Fatalf("GroupByResource() error = %v", err)
	}

	needed := map[string]bool{}
	for _, rg := range resources {
		needed[rg.Message.FullName] = true
	}
	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			needed[m.InputName] = true
			needed[m.OutputName] = true
		}
	}

	for fullName := range needed {
		if _, ok := exampleJSON[fullName]; !ok {
			t.Errorf("message %q is a resource or RPC request/response type but has no entry in exampleJSON", fullName)
		}
	}
}
