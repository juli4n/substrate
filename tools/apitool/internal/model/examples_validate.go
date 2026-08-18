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
	"encoding/json"
	"fmt"
)

// wellKnownJSONStringTypes are message types protojson encodes as a bare
// JSON string rather than by their own field layout - checking structural
// shape against their .proto fields would be wrong for these. They're
// imported well-known types, so they never appear in an API's Messages.
var wellKnownJSONStringTypes = map[string]bool{
	"google.protobuf.Timestamp": true,
	"google.protobuf.FieldMask": true,
	"google.protobuf.Duration":  true,
}

// ValidateExampleJSON checks every hand-authored example under examples/
// (see exampleJSON) against api's messages: every JSON key must name a real
// field, by its protojson name, and every value's shape must match that
// field's kind. Examples aren't required to be exhaustive - only the
// fields they do include must be real and correctly shaped. Returns one
// error per problem found; nil if every example checks out.
func ValidateExampleJSON(api *API) []error {
	messagesByName := make(map[string]*Message, len(api.Messages))
	for i := range api.Messages {
		messagesByName[api.Messages[i].FullName] = &api.Messages[i]
	}

	var errs []error
	for fullName, raw := range exampleJSON {
		if err := validateExample(messagesByName, fullName, raw); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// validateExample checks one example (raw protojson) against the message
// named fullName, resolved from messagesByName.
func validateExample(messagesByName map[string]*Message, fullName, raw string) error {
	md, ok := messagesByName[fullName]
	if !ok {
		return fmt.Errorf("example %s.json: message %q not found in the API", fullName, fullName)
	}

	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return fmt.Errorf("example %s.json: invalid JSON: %w", fullName, err)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("example %s.json: top-level example must be a JSON object, got %T", fullName, value)
	}
	if err := checkObjectAgainstMessage(messagesByName, obj, md); err != nil {
		return fmt.Errorf("example %s.json: %w", fullName, err)
	}
	return nil
}

// checkObjectAgainstMessage reports an error for the first key in obj that
// doesn't name a real field of md (by its protojson name), or whose value's
// JSON shape doesn't match that field's kind.
func checkObjectAgainstMessage(messagesByName map[string]*Message, obj map[string]any, md *Message) error {
	fieldsByJSONName := make(map[string]Field, len(md.Fields))
	for _, f := range md.Fields {
		fieldsByJSONName[f.JSONName] = f
	}
	for key, val := range obj {
		f, ok := fieldsByJSONName[key]
		if !ok {
			return fmt.Errorf("%s: field %q not found", md.FullName, key)
		}
		if err := checkValueAgainstField(messagesByName, val, f); err != nil {
			return fmt.Errorf("%s.%s: %w", md.FullName, key, err)
		}
	}
	return nil
}

func checkValueAgainstField(messagesByName map[string]*Message, val any, f Field) error {
	switch {
	case f.IsMap:
		obj, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("expected a JSON object for a map field, got %T", val)
		}
		if f.MapValueKind != "message" {
			return nil
		}
		for k, v := range obj {
			if err := checkMessageFieldValue(messagesByName, v, f.MapValueFullName); err != nil {
				return fmt.Errorf("map value %q: %w", k, err)
			}
		}
		return nil
	case f.Repeated:
		arr, ok := val.([]any)
		if !ok {
			return fmt.Errorf("expected a JSON array for a repeated field, got %T", val)
		}
		if f.TypeKind != "message" {
			return nil
		}
		for i, elem := range arr {
			if err := checkMessageFieldValue(messagesByName, elem, f.TypeFullName); err != nil {
				return fmt.Errorf("element %d: %w", i, err)
			}
		}
		return nil
	case f.TypeKind == "message":
		return checkMessageFieldValue(messagesByName, val, f.TypeFullName)
	default:
		return nil // scalar or enum: any JSON scalar shape is acceptable here.
	}
}

func checkMessageFieldValue(messagesByName map[string]*Message, val any, typeFullName string) error {
	if wellKnownJSONStringTypes[typeFullName] {
		if _, ok := val.(string); !ok {
			return fmt.Errorf("%s is a well-known type protojson encodes as a string, got %T", typeFullName, val)
		}
		return nil
	}
	obj, ok := val.(map[string]any)
	if !ok {
		return fmt.Errorf("expected a JSON object for message type %s, got %T", typeFullName, val)
	}
	md, ok := messagesByName[typeFullName]
	if !ok {
		return fmt.Errorf("message type %s not found in the API", typeFullName)
	}
	return checkObjectAgainstMessage(messagesByName, obj, md)
}
