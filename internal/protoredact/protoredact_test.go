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

package protoredact

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
)

type fieldOpt func(*descriptorpb.FieldDescriptorProto)

func field(name string, num int32, typ descriptorpb.FieldDescriptorProto_Type, opts ...fieldOpt) *descriptorpb.FieldDescriptorProto {
	fd := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(num),
		Type:     typ.Enum(),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		JsonName: proto.String(name),
	}
	for _, o := range opts {
		o(fd)
	}
	return fd
}

func ofMessage(typeName string) fieldOpt {
	return func(fd *descriptorpb.FieldDescriptorProto) { fd.TypeName = proto.String(typeName) }
}

func repeated() fieldOpt {
	return func(fd *descriptorpb.FieldDescriptorProto) {
		fd.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	}
}

func redacted() fieldOpt {
	return func(fd *descriptorpb.FieldDescriptorProto) {
		fd.Options = &descriptorpb.FieldOptions{DebugRedact: proto.Bool(true)}
	}
}

func inOneof(index int32) fieldOpt {
	return func(fd *descriptorpb.FieldDescriptorProto) { fd.OneofIndex = proto.Int32(index) }
}

const (
	tString  = descriptorpb.FieldDescriptorProto_TYPE_STRING
	tBytes   = descriptorpb.FieldDescriptorProto_TYPE_BYTES
	tMessage = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
)

func testSchema(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("redacttest.proto"),
		Package: proto.String("redacttest"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Inner"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("password", 1, tString, redacted()),
					field("note", 2, tString),
				},
			},
			{
				Name: proto.String("Outer"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("token", 1, tString, redacted()),
					field("name", 2, tString),
					field("blob", 3, tBytes, redacted()),
					field("secrets", 4, tString, repeated(), redacted()),
					field("inner", 5, tMessage, ofMessage(".redacttest.Inner")),
					field("inners", 6, tMessage, ofMessage(".redacttest.Inner"), repeated()),
					field("inner_map", 7, tMessage, ofMessage(".redacttest.Outer.InnerMapEntry"), repeated()),
					field("whole", 8, tMessage, ofMessage(".redacttest.Inner"), redacted()),
					field("plain_choice", 9, tString, inOneof(0)),
					field("secret_choice", 10, tString, inOneof(0), redacted()),
				},
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("choice")}},
				NestedType: []*descriptorpb.DescriptorProto{{
					Name: proto.String("InnerMapEntry"),
					Field: []*descriptorpb.FieldDescriptorProto{
						field("key", 1, tString),
						field("value", 2, tMessage, ofMessage(".redacttest.Inner")),
					},
					Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
				}},
			},
			{
				Name: proto.String("Leaf"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("b", 1, tString),
				},
			},
			{
				Name: proto.String("Plain"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("a", 1, tString),
					field("leaf", 2, tMessage, ofMessage(".redacttest.Leaf")),
				},
			},
			{
				// Self-recursive, redacted field.
				Name: proto.String("Node"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("next", 1, tMessage, ofMessage(".redacttest.Node")),
					field("secret", 2, tString, redacted()),
				},
			},
			{
				// Mutually recursive, nothing redacted.
				Name: proto.String("Ring"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("r", 1, tMessage, ofMessage(".redacttest.Ring2")),
					field("x", 2, tString),
				},
			},
			{
				Name: proto.String("Ring2"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("r", 1, tMessage, ofMessage(".redacttest.Ring")),
				},
			},
		},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("protodesc.NewFile: %v", err)
	}
	return fd
}

type schema struct {
	t    *testing.T
	file protoreflect.FileDescriptor
}

func (s schema) msg(name protoreflect.Name) *dynamicpb.Message {
	md := s.file.Messages().ByName(name)
	if md == nil {
		s.t.Fatalf("no message %q in test schema", name)
	}
	return dynamicpb.NewMessage(md)
}

func fdOf(m protoreflect.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return m.Descriptor().Fields().ByName(name)
}

func setString(m protoreflect.Message, name protoreflect.Name, v string) {
	m.Set(fdOf(m, name), protoreflect.ValueOfString(v))
}

func getString(m protoreflect.Message, name protoreflect.Name) string {
	return m.Get(fdOf(m, name)).String()
}

func newInner(s schema, password, note string) *dynamicpb.Message {
	in := s.msg("Inner")
	setString(in, "password", password)
	setString(in, "note", note)
	return in
}

func TestRedactOuter(t *testing.T) {
	s := schema{t, testSchema(t)}
	in := s.msg("Outer")
	setString(in, "token", "tok")
	setString(in, "name", "visible")
	in.Set(fdOf(in, "blob"), protoreflect.ValueOfBytes([]byte("bytes")))
	secrets := in.Mutable(fdOf(in, "secrets")).List()
	secrets.Append(protoreflect.ValueOfString("s1"))
	in.Set(fdOf(in, "inner"), protoreflect.ValueOfMessage(newInner(s, "p1", "n1")))
	inners := in.Mutable(fdOf(in, "inners")).List()
	inners.Append(protoreflect.ValueOfMessage(newInner(s, "p2", "n2")))
	innerMap := in.Mutable(fdOf(in, "inner_map")).Map()
	innerMap.Set(protoreflect.ValueOfString("k").MapKey(), protoreflect.ValueOfMessage(newInner(s, "p3", "n3")))
	in.Set(fdOf(in, "whole"), protoreflect.ValueOfMessage(newInner(s, "p4", "n4")))
	setString(in, "secret_choice", "chosen")

	before := proto.Clone(in)
	got := Redact(in).ProtoReflect()

	if !proto.Equal(before, in) {
		t.Fatalf("Redact modified its input:\n got %v\nwant %v", in, before)
	}
	if got == in.ProtoReflect() {
		t.Fatalf("Redact returned its input for a message with redacted fields")
	}

	if v := getString(got, "token"); v != placeholder {
		t.Errorf("token = %q, want %q", v, placeholder)
	}
	if v := getString(got, "name"); v != "visible" {
		t.Errorf("name = %q, want untouched", v)
	}
	if got.Has(fdOf(got, "blob")) {
		t.Errorf("blob still set")
	}
	if got.Has(fdOf(got, "secrets")) {
		t.Errorf("secrets still set")
	}
	if got.Has(fdOf(got, "whole")) {
		t.Errorf("whole (a redacted message field) still set")
	}
	if got.Has(fdOf(got, "secret_choice")) {
		t.Errorf("secret_choice (a redacted oneof member) still set")
	}

	inner := got.Get(fdOf(got, "inner")).Message()
	if v := getString(inner, "password"); v != placeholder {
		t.Errorf("inner.password = %q, want %q", v, placeholder)
	}
	if v := getString(inner, "note"); v != "n1" {
		t.Errorf("inner.note = %q, want untouched", v)
	}
	el := got.Get(fdOf(got, "inners")).List().Get(0).Message()
	if v := getString(el, "password"); v != placeholder {
		t.Errorf("inners[0].password = %q, want %q", v, placeholder)
	}
	mv := got.Get(fdOf(got, "inner_map")).Map().Get(protoreflect.ValueOfString("k").MapKey()).Message()
	if v := getString(mv, "password"); v != placeholder {
		t.Errorf("inner_map[k].password = %q, want %q", v, placeholder)
	}
	if v := getString(mv, "note"); v != "n3" {
		t.Errorf("inner_map[k].note = %q, want untouched", v)
	}
}

func TestRedactReturnsInputWhenNothingToRedact(t *testing.T) {
	s := schema{t, testSchema(t)}
	in := s.msg("Plain")
	setString(in, "a", "keep")
	leaf := s.msg("Leaf")
	setString(leaf, "b", "keep too")
	in.Set(fdOf(in, "leaf"), protoreflect.ValueOfMessage(leaf))

	got := Redact(in)
	if got != proto.Message(in) {
		t.Fatalf("Redact cloned a message with no redacted fields")
	}
}

func TestRedactRecursiveSchema(t *testing.T) {
	s := schema{t, testSchema(t)}
	deep := s.msg("Node")
	setString(deep, "secret", "deep")
	mid := s.msg("Node")
	mid.Set(fdOf(mid, "next"), protoreflect.ValueOfMessage(deep))
	top := s.msg("Node")
	top.Set(fdOf(top, "next"), protoreflect.ValueOfMessage(mid))

	got := Redact(top).ProtoReflect()
	gotDeep := got.Get(fdOf(got, "next")).Message().Get(fdOf(got, "next")).Message()
	if v := getString(gotDeep, "secret"); v != placeholder {
		t.Errorf("next.next.secret = %q, want %q", v, placeholder)
	}
}

func TestHasRedactedFields(t *testing.T) {
	file := testSchema(t)
	for name, want := range map[protoreflect.Name]bool{
		"Inner": true,
		"Outer": true,
		"Leaf":  false,
		"Plain": false,
		"Node":  true,
		"Ring":  false,
		"Ring2": false,
	} {
		md := file.Messages().ByName(name)
		if got := HasRedactedFields(md); got != want {
			t.Errorf("HasRedactedFields(%s) = %v, want %v", name, got, want)
		}
		if got := HasRedactedFields(md); got != want {
			t.Errorf("HasRedactedFields(%s) cached = %v, want %v", name, got, want)
		}
	}
}

func TestRedactRepositoryMessages(t *testing.T) {
	for name, tc := range map[string]struct {
		in   proto.Message
		want proto.Message
	}{
		"ateapi MintActorJWTResponse": {
			in:   &ateapipb.MintActorJWTResponse{ActorJwt: "eyJ.secret"},
			want: &ateapipb.MintActorJWTResponse{ActorJwt: placeholder},
		},
		"ateapi EnvVar": {
			in:   &ateapipb.EnvVar{Name: "API_KEY", Value: "sk-secret"},
			want: &ateapipb.EnvVar{Name: "API_KEY", Value: placeholder},
		},
		"atelet RunRequest env": {
			in: &ateletpb.RunRequest{Spec: &ateletpb.WorkloadSpec{Containers: []*ateletpb.Container{
				{Name: "main", Env: []*ateletpb.EnvEntry{{Name: "API_KEY", Value: "sk-secret"}}},
			}}},
			want: &ateletpb.RunRequest{Spec: &ateletpb.WorkloadSpec{Containers: []*ateletpb.Container{
				{Name: "main", Env: []*ateletpb.EnvEntry{{Name: "API_KEY", Value: placeholder}}},
			}}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := proto.Clone(tc.in)
			got := Redact(tc.in)
			if !proto.Equal(got, tc.want) {
				t.Errorf("Redact() = %v, want %v", got, tc.want)
			}
			if !proto.Equal(tc.in, before) {
				t.Errorf("Redact modified its input: %v", tc.in)
			}
		})
	}
}

func TestRedactNil(t *testing.T) {
	if got := Redact(nil); got != nil {
		t.Errorf("Redact(nil) = %v, want nil", got)
	}
}
