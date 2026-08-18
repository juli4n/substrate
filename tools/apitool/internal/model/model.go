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

// Package model walks a protoreflect.FileDescriptor into a plain Go data
// model, with no protobuf types leaking into consumers.
package model

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// API is the whole documented surface of one proto file.
type API struct {
	Services []Service
	Messages []Message // top-level and nested, flattened; see Message.ParentFullName
	Enums    []Enum    // top-level and nested, flattened; see Enum.ParentFullName
}

type Service struct {
	FullName string
	Name     string
	Comment  string
	Methods  []Method
}

type Method struct {
	Name       string
	Comment    string
	InputName  string // full name of the input message, for linking
	OutputName string // full name of the output message, for linking

	ServiceFullName string
	ServiceName     string
}

type Message struct {
	FullName       string
	Name           string // "Actor", or "Actor.Status" for a nested type
	ParentFullName string // "" for a top-level message
	Comment        string
	Fields         []Field

	// Example is a hand-authored protojson value for this message (see
	// examples.go). Empty if none exists.
	Example string
}

type Field struct {
	Name string
	// JSONName is the field's protojson name (e.g. "createTime" for
	// create_time), for matching against hand-authored example JSON - see
	// ValidateExampleJSON.
	JSONName string
	Number   int32
	Comment  string

	// Repeated is true for a `repeated` field, not a map - see IsMap.
	Repeated bool
	// IsMap is true for a map field. Repeated is always false for these -
	// protoreflect doesn't consider a map field a list.
	IsMap bool

	// TypeDisplay is always a ready-to-render string, e.g. "string",
	// "repeated ExternalVolume", "map<string, string>", "SnapshotContentScope".
	TypeDisplay string
	// TypeFullName and TypeKind ("message" or "enum") are set only for a
	// singular or repeated message/enum field. Empty for scalars and maps -
	// see MapValueFullName/MapValueKind for a map's value type.
	TypeFullName string
	TypeKind     string
	// TypeIsExternal is true when the type isn't declared in this proto
	// file (e.g. an imported well-known type).
	TypeIsExternal bool

	// MapValueFullName and MapValueKind mirror TypeFullName/TypeKind, but
	// for a map field's value type. Both are empty for a scalar-valued map
	// (e.g. map<string, string>) - check IsMap, not these, to detect a map
	// field.
	MapValueFullName string
	MapValueKind     string

	// OneofName is set only for a member of a real, explicit `oneof` group.
	OneofName string
	// Proto3Optional is true for a proto3 `optional` scalar - protoreflect
	// models it as a synthetic oneof, not a real one.
	Proto3Optional bool
}

type Enum struct {
	FullName       string
	Name           string
	ParentFullName string
	Comment        string
	Values         []EnumValue
}

type EnumValue struct {
	Name    string
	Number  int32
	Comment string
}

// Build walks fd into an API. Comments are populated only if fd came from
// parser.BuildFileDescriptor.
func Build(fd protoreflect.FileDescriptor) *API {
	api := &API{}

	services := fd.Services()
	for i := range services.Len() {
		api.Services = append(api.Services, buildService(fd, services.Get(i)))
	}

	collectMessages(fd, fd.Messages(), api)
	collectEnums(fd, fd.Enums(), api)

	return api
}

// FilterToService returns a copy of api with only the service named
// serviceName, plus every message/enum reachable from its methods'
// input/output types (including through map values). Empty result if
// serviceName doesn't exist.
func FilterToService(api *API, serviceName string) *API {
	var svc *Service
	for i := range api.Services {
		if api.Services[i].Name == serviceName {
			svc = &api.Services[i]
			break
		}
	}
	if svc == nil {
		return &API{}
	}

	messagesByName := make(map[string]*Message, len(api.Messages))
	for i := range api.Messages {
		messagesByName[api.Messages[i].FullName] = &api.Messages[i]
	}

	reachableMessages := map[string]bool{}
	reachableEnums := map[string]bool{}

	var visitMessage func(name string)
	visitMessage = func(name string) {
		if reachableMessages[name] {
			return
		}
		msg, ok := messagesByName[name]
		if !ok {
			return
		}
		reachableMessages[name] = true
		for _, f := range msg.Fields {
			switch f.TypeKind {
			case "message":
				visitMessage(f.TypeFullName)
			case "enum":
				reachableEnums[f.TypeFullName] = true
			}
			switch f.MapValueKind {
			case "message":
				visitMessage(f.MapValueFullName)
			case "enum":
				reachableEnums[f.MapValueFullName] = true
			}
		}
	}

	for _, m := range svc.Methods {
		visitMessage(m.InputName)
		visitMessage(m.OutputName)
	}

	out := &API{Services: []Service{*svc}}
	for i := range api.Messages {
		if reachableMessages[api.Messages[i].FullName] {
			out.Messages = append(out.Messages, api.Messages[i])
		}
	}
	for i := range api.Enums {
		if reachableEnums[api.Enums[i].FullName] {
			out.Enums = append(out.Enums, api.Enums[i])
		}
	}
	return out
}

func collectMessages(fd protoreflect.FileDescriptor, mds protoreflect.MessageDescriptors, api *API) {
	for i := range mds.Len() {
		md := mds.Get(i)
		if md.IsMapEntry() {
			continue // synthetic per-map-field type, never a documented message.
		}
		api.Messages = append(api.Messages, buildMessage(fd, md))
		collectMessages(fd, md.Messages(), api)
		collectEnums(fd, md.Enums(), api)
	}
}

func collectEnums(fd protoreflect.FileDescriptor, eds protoreflect.EnumDescriptors, api *API) {
	for i := range eds.Len() {
		api.Enums = append(api.Enums, buildEnum(fd, eds.Get(i)))
	}
}

func buildService(fd protoreflect.FileDescriptor, sd protoreflect.ServiceDescriptor) Service {
	svc := Service{
		FullName: string(sd.FullName()),
		Name:     string(sd.Name()),
		Comment:  comment(fd, sd),
	}
	methods := sd.Methods()
	for i := range methods.Len() {
		md := methods.Get(i)
		svc.Methods = append(svc.Methods, Method{
			Name:            string(md.Name()),
			Comment:         comment(fd, md),
			InputName:       string(md.Input().FullName()),
			OutputName:      string(md.Output().FullName()),
			ServiceFullName: svc.FullName,
			ServiceName:     svc.Name,
		})
	}
	return svc
}

func buildMessage(fd protoreflect.FileDescriptor, md protoreflect.MessageDescriptor) Message {
	m := Message{
		FullName:       string(md.FullName()),
		Name:           shortName(fd, md.FullName()),
		ParentFullName: string(parentFullName(md)),
		Comment:        comment(fd, md),
		Example:        exampleJSON[string(md.FullName())],
	}
	fields := md.Fields()
	for i := range fields.Len() {
		m.Fields = append(m.Fields, buildField(fd, fields.Get(i)))
	}
	return m
}

func buildField(fd protoreflect.FileDescriptor, field protoreflect.FieldDescriptor) Field {
	f := Field{
		Name:     string(field.Name()),
		JSONName: field.JSONName(),
		Number:   int32(field.Number()),
		Comment:  comment(fd, field),
		Repeated: field.IsList(),
	}

	switch {
	case field.IsMap():
		f.IsMap = true
		f.TypeDisplay = fmt.Sprintf("map<%s, %s>", scalarOrTypeName(fd, field.MapKey()), scalarOrTypeName(fd, field.MapValue()))
		switch field.MapValue().Kind() {
		case protoreflect.MessageKind, protoreflect.GroupKind:
			f.MapValueFullName = string(field.MapValue().Message().FullName())
			f.MapValueKind = "message"
		case protoreflect.EnumKind:
			f.MapValueFullName = string(field.MapValue().Enum().FullName())
			f.MapValueKind = "enum"
		}
	case field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind:
		name := shortName(fd, field.Message().FullName())
		f.TypeFullName = string(field.Message().FullName())
		f.TypeKind = "message"
		f.TypeIsExternal = field.Message().ParentFile() != fd
		f.TypeDisplay = repeatedPrefix(field) + name
	case field.Kind() == protoreflect.EnumKind:
		name := shortName(fd, field.Enum().FullName())
		f.TypeFullName = string(field.Enum().FullName())
		f.TypeKind = "enum"
		f.TypeIsExternal = field.Enum().ParentFile() != fd
		f.TypeDisplay = repeatedPrefix(field) + name
	default:
		f.TypeDisplay = repeatedPrefix(field) + field.Kind().String()
	}

	if oneof := field.ContainingOneof(); oneof != nil {
		if oneof.IsSynthetic() {
			f.Proto3Optional = true
		} else {
			f.OneofName = string(oneof.Name())
		}
	}

	return f
}

func buildEnum(fd protoreflect.FileDescriptor, ed protoreflect.EnumDescriptor) Enum {
	e := Enum{
		FullName:       string(ed.FullName()),
		Name:           shortName(fd, ed.FullName()),
		ParentFullName: string(parentFullName(ed)),
		Comment:        comment(fd, ed),
	}
	values := ed.Values()
	for i := range values.Len() {
		v := values.Get(i)
		e.Values = append(e.Values, EnumValue{
			Name:    string(v.Name()),
			Number:  int32(v.Number()),
			Comment: comment(fd, v),
		})
	}
	return e
}

func repeatedPrefix(field protoreflect.FieldDescriptor) string {
	if field.IsList() {
		return "repeated "
	}
	return ""
}

// scalarOrTypeName names a map key or value field's type: the scalar kind
// name, or the message/enum's short name.
func scalarOrTypeName(fd protoreflect.FileDescriptor, mapField protoreflect.FieldDescriptor) string {
	switch mapField.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return shortName(fd, mapField.Message().FullName())
	case protoreflect.EnumKind:
		return shortName(fd, mapField.Enum().FullName())
	default:
		return mapField.Kind().String()
	}
}

// shortName strips fd's package prefix from a full name, e.g.
// "ateapi.Actor.Status" -> "Actor.Status".
func shortName(fd protoreflect.FileDescriptor, full protoreflect.FullName) string {
	return strings.TrimPrefix(string(full), string(fd.Package())+".")
}

// parentFullName returns d's enclosing message's full name, or "" if d is
// declared at the top level of the file.
func parentFullName(d protoreflect.Descriptor) protoreflect.FullName {
	if md, ok := d.Parent().(protoreflect.MessageDescriptor); ok {
		return md.FullName()
	}
	return ""
}

func comment(fd protoreflect.FileDescriptor, d protoreflect.Descriptor) string {
	return strings.TrimSpace(fd.SourceLocations().ByDescriptor(d).LeadingComments)
}

// ResourceGroup pairs a message with every method (across services) that
// resourceForMethodName matches it to, in encounter order.
type ResourceGroup struct {
	Message Message
	Methods []Method
}

// GroupByResource partitions api's methods by resource, matching each
// method's name against resourceNames (see resourceForMethodName), in
// api.Messages order. Fails if a method matches no resource name or
// multiple equally-specific ones, or if a matched name has no corresponding
// message - there's no proto annotation backing this, so these are the
// only checks.
func GroupByResource(api *API) ([]ResourceGroup, error) {
	// Pass 1: resolve every method to a resource name, and collect the
	// names actually referenced.
	methodResource := make(map[string]string, len(api.Services))
	referencedNames := map[string]bool{}
	for _, svc := range api.Services {
		for _, method := range svc.Methods {
			resource, err := resourceForMethodName(method.Name)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", svc.Name, method.Name, err)
			}
			methodResource[svc.Name+"."+method.Name] = resource
			referencedNames[resource] = true
		}
	}

	// Pass 2: one group per referenced resource, in api.Messages order,
	// deleting each name as matched so any left over names an unknown
	// resource.
	groups := make([]ResourceGroup, 0, len(referencedNames))
	for _, m := range api.Messages {
		if referencedNames[m.Name] {
			groups = append(groups, ResourceGroup{Message: m})
			delete(referencedNames, m.Name)
		}
	}
	for name := range referencedNames {
		return nil, fmt.Errorf("resource name %q, matched from a method name, has no corresponding message in this API", name)
	}

	// Pass 3: append each method to its group. groups's length is fixed by
	// now, so taking addresses into it is safe.
	byName := make(map[string]*ResourceGroup, len(groups))
	for i := range groups {
		byName[groups[i].Message.Name] = &groups[i]
	}
	for _, svc := range api.Services {
		for _, method := range svc.Methods {
			resource := methodResource[svc.Name+"."+method.Name]
			byName[resource].Methods = append(byName[resource].Methods, method)
		}
	}

	return groups, nil
}

// AttrNode is one row of a message's field list, recursively including
// child fields when the type expands. Children is nil for scalars, enums,
// and external types.
type AttrNode struct {
	Field Field
	// Path is the dotted field path from the root (e.g.
	// "source_snapshot.tag.atespace"), for identifying deeply nested rows.
	Path     string
	Children []AttrNode
}

// maxAttrTreeDepth bounds recursion independent of the cycle guard below.
const maxAttrTreeDepth = 6

// BuildAttrTree turns fields into an AttrNode tree, expanding each
// message-typed field (or map value) against api's messages. Guards
// against a cyclic schema by never re-expanding a type already on the
// current path.
func BuildAttrTree(api *API, fields []Field) []AttrNode {
	messagesByName := make(map[string]*Message, len(api.Messages))
	for i := range api.Messages {
		messagesByName[api.Messages[i].FullName] = &api.Messages[i]
	}
	return buildAttrNodes(messagesByName, fields, "", map[string]bool{}, 0)
}

func buildAttrNodes(byName map[string]*Message, fields []Field, pathPrefix string, ancestors map[string]bool, depth int) []AttrNode {
	nodes := make([]AttrNode, len(fields))
	for i, f := range fields {
		path := f.Name
		if pathPrefix != "" {
			path = pathPrefix + "." + f.Name
		}
		nodes[i] = AttrNode{Field: f, Path: path}

		var childTypeName string
		switch {
		case f.TypeKind == "message":
			childTypeName = f.TypeFullName
		case f.MapValueKind == "message":
			childTypeName = f.MapValueFullName
		default:
			continue
		}
		if depth >= maxAttrTreeDepth || ancestors[childTypeName] {
			continue
		}
		child, ok := byName[childTypeName]
		if !ok {
			continue // external/well-known type: not expandable.
		}

		nextAncestors := make(map[string]bool, len(ancestors)+1)
		for k := range ancestors {
			nextAncestors[k] = true
		}
		nextAncestors[childTypeName] = true
		nodes[i].Children = buildAttrNodes(byName, child.Fields, path, nextAncestors, depth+1)
	}
	return nodes
}
