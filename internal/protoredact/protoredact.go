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

// Package protoredact clears fields marked `[debug_redact = true]` from
// protobuf messages.
package protoredact

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// placeholder replaces a redacted singular string. Other redacted fields are cleared.
const placeholder = "[REDACTED]"

// typeHasRedactedFields caches HasRedactedFields per message type.
var typeHasRedactedFields sync.Map // map[protoreflect.FullName]bool

// Redact returns a copy of m with every redacted field cleared, recursively.
// It returns m itself when m's type has no redacted field.
func Redact(m proto.Message) proto.Message {
	if m == nil {
		return nil
	}
	rm := m.ProtoReflect()
	if !rm.IsValid() || !HasRedactedFields(rm.Descriptor()) {
		return m
	}
	clone := proto.Clone(m)
	redact(clone.ProtoReflect())
	return clone
}

// HasRedactedFields reports whether md or any message type reachable from it
// has a redacted field.
func HasRedactedFields(md protoreflect.MessageDescriptor) bool {
	name := md.FullName()
	if v, ok := typeHasRedactedFields.Load(name); ok {
		return v.(bool)
	}
	// Only the root is memoized: a type visited inside an open cycle may
	// look clean before the cycle closes.
	found := walkHasRedacted(md, map[protoreflect.FullName]bool{})
	typeHasRedactedFields.Store(name, found)
	return found
}

func walkHasRedacted(md protoreflect.MessageDescriptor, visiting map[protoreflect.FullName]bool) bool {
	if v, ok := typeHasRedactedFields.Load(md.FullName()); ok && v.(bool) {
		return true
	}
	if visiting[md.FullName()] {
		return false
	}
	visiting[md.FullName()] = true
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if isRedacted(fd) {
			return true
		}
		if child := childMessage(fd); child != nil && walkHasRedacted(child, visiting) {
			return true
		}
	}
	return false
}

// childMessage returns fd's message type (the value type for maps), or nil.
func childMessage(fd protoreflect.FieldDescriptor) protoreflect.MessageDescriptor {
	if fd.IsMap() {
		return fd.MapValue().Message()
	}
	return fd.Message()
}

func isRedacted(fd protoreflect.FieldDescriptor) bool {
	opts, ok := fd.Options().(*descriptorpb.FieldOptions)
	return ok && opts.GetDebugRedact()
}

func redact(m protoreflect.Message) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if isRedacted(fd) {
			if fd.Kind() == protoreflect.StringKind && !fd.IsList() && !fd.IsMap() {
				m.Set(fd, protoreflect.ValueOfString(placeholder))
			} else {
				m.Clear(fd)
			}
			return true
		}
		child := childMessage(fd)
		if child == nil || !HasRedactedFields(child) {
			return true
		}
		switch {
		case fd.IsMap():
			v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
				redact(mv.Message())
				return true
			})
		case fd.IsList():
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				redact(list.Get(i).Message())
			}
		default:
			redact(v.Message())
		}
		return true
	})
}
