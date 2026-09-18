// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package contextlogging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/agent-substrate/substrate/internal/ateattr"
)

const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
)

func spanContext(t *testing.T, flags trace.TraceFlags) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex(testTraceID)
	if err != nil {
		t.Fatalf("TraceIDFromHex(%q): %v", testTraceID, err)
	}
	spanID, err := trace.SpanIDFromHex(testSpanID)
	if err != nil {
		t.Fatalf("SpanIDFromHex(%q): %v", testSpanID, err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: flags})
}

func TestHandleTraceCorrelation(t *testing.T) {
	tests := []struct {
		name           string
		ctx            func(t *testing.T) context.Context
		wantTraceID    string
		wantSpanID     string
		wantTraceFlags string
	}{
		{
			name: "no span in context",
			ctx:  func(*testing.T) context.Context { return context.Background() },
		},
		{
			name: "invalid span context contributes nothing",
			ctx: func(*testing.T) context.Context {
				return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{}))
			},
		},
		{
			name: "sampled span",
			ctx: func(t *testing.T) context.Context {
				return trace.ContextWithSpanContext(context.Background(), spanContext(t, trace.FlagsSampled))
			},
			wantTraceID:    testTraceID,
			wantSpanID:     testSpanID,
			wantTraceFlags: "01",
		},
		{
			// An unsampled record still says which request it belongs to; the flags
			// are what tells a reader why the trace is not in the backend.
			name: "unsampled span still correlates",
			ctx: func(t *testing.T) context.Context {
				return trace.ContextWithSpanContext(context.Background(), spanContext(t, 0))
			},
			wantTraceID:    testTraceID,
			wantSpanID:     testSpanID,
			wantTraceFlags: "00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := slog.New(NewHandler(slog.NewJSONHandler(&buf, nil)))
			logger.InfoContext(tt.ctx(t), "something happened")

			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("failed to parse log record %q: %v", buf.String(), err)
			}

			for field, want := range map[string]string{
				ateattr.LogTraceIDField:    tt.wantTraceID,
				ateattr.LogSpanIDField:     tt.wantSpanID,
				ateattr.LogTraceFlagsField: tt.wantTraceFlags,
			} {
				got, present := rec[field]
				if want == "" {
					if present {
						t.Errorf("%s = %v, want absent", field, got)
					}
					continue
				}
				if got != want {
					t.Errorf("%s = %v, want %q", field, got, want)
				}
			}
		})
	}
}

// TestHandleUngroupedKeepsTraceFieldsTopLevel pins the placement the spec
// requires: a collector only lifts these onto the log record from the top level.
func TestHandleUngroupedKeepsTraceFieldsTopLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&buf, nil)))
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext(t, trace.FlagsSampled))
	logger.With(slog.String("component", "atelet")).InfoContext(ctx, "something happened", slog.String("id", "abc"))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("failed to parse log record %q: %v", buf.String(), err)
	}
	if rec[ateattr.LogTraceIDField] != testTraceID {
		t.Errorf("%s = %v, want %q at the top level, got record %v", ateattr.LogTraceIDField, rec[ateattr.LogTraceIDField], testTraceID, rec)
	}
}

type recordingHandler struct {
	records []slog.Record
	attrs   []slog.Attr
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recordingHandler{attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}
func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

func redactSchema(t testing.TB) protoreflect.FileDescriptor {
	t.Helper()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("contextlogging_redact_test.proto"),
		Package: proto.String("contextloggingtest"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Secret"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("token"), Number: proto.Int32(1), Type: str, Label: opt, JsonName: proto.String("token"),
						Options: &descriptorpb.FieldOptions{DebugRedact: proto.Bool(true)}},
					{Name: proto.String("name"), Number: proto.Int32(2), Type: str, Label: opt, JsonName: proto.String("name")},
				},
			},
			{
				Name: proto.String("Plain"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("name"), Number: proto.Int32(1), Type: str, Label: opt, JsonName: proto.String("name")},
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

func newMessage(t testing.TB, file protoreflect.FileDescriptor, name protoreflect.Name, fields map[protoreflect.Name]string) *dynamicpb.Message {
	t.Helper()
	md := file.Messages().ByName(name)
	if md == nil {
		t.Fatalf("no message %q", name)
	}
	m := dynamicpb.NewMessage(md)
	for k, v := range fields {
		m.Set(md.Fields().ByName(k), protoreflect.ValueOfString(v))
	}
	return m
}

func stringField(m proto.Message, name protoreflect.Name) string {
	rm := m.ProtoReflect()
	return rm.Get(rm.Descriptor().Fields().ByName(name)).String()
}

func attrsOf(r slog.Record) map[string]slog.Value {
	out := map[string]slog.Value{}
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value
		return true
	})
	return out
}

func TestHandleRedactsProtoAttrs(t *testing.T) {
	file := redactSchema(t)
	secret := newMessage(t, file, "Secret", map[protoreflect.Name]string{"token": "tok", "name": "visible"})
	plain := newMessage(t, file, "Plain", map[protoreflect.Name]string{"name": "plain"})

	rec := &recordingHandler{}
	logger := slog.New(NewHandler(rec))
	logger.Info("rpc",
		slog.Any("req", secret),
		slog.Any("resp", plain),
		slog.String("method", "/x/Y"),
		slog.Group("nested", slog.Any("inner", secret)),
	)

	if len(rec.records) != 1 {
		t.Fatalf("got %d records, want 1", len(rec.records))
	}
	attrs := attrsOf(rec.records[0])

	got, ok := attrs["req"].Any().(proto.Message)
	if !ok {
		t.Fatalf("req attr is %T, want proto.Message", attrs["req"].Any())
	}
	if v := stringField(got, "token"); v != "[REDACTED]" {
		t.Errorf("req.token = %q, want %q", v, "[REDACTED]")
	}
	if v := stringField(got, "name"); v != "visible" {
		t.Errorf("req.name = %q, want untouched", v)
	}
	if v := stringField(secret, "token"); v != "tok" {
		t.Errorf("handler mutated the caller's message: token = %q", v)
	}

	if attrs["resp"].Any() != proto.Message(plain) {
		t.Errorf("resp (no redacted fields) was replaced; want the caller's message passed through")
	}
	if attrs["method"].String() != "/x/Y" {
		t.Errorf("method = %q, want untouched", attrs["method"].String())
	}

	var inner proto.Message
	for _, a := range attrs["nested"].Group() {
		if a.Key == "inner" {
			inner, _ = a.Value.Any().(proto.Message)
		}
	}
	if inner == nil {
		t.Fatalf("nested.inner missing from group %v", attrs["nested"])
	}
	if v := stringField(inner, "token"); v != "[REDACTED]" {
		t.Errorf("nested.inner.token = %q, want %q", v, "[REDACTED]")
	}
}

func TestHandleLeavesRecordsWithoutProtoAlone(t *testing.T) {
	rec := &recordingHandler{}
	logger := slog.New(NewHandler(rec))
	logger.Info("plain", slog.String("k", "v"), slog.Int("n", 1))

	if len(rec.records) != 1 {
		t.Fatalf("got %d records, want 1", len(rec.records))
	}
	attrs := attrsOf(rec.records[0])
	if attrs["k"].String() != "v" || attrs["n"].Int64() != 1 {
		t.Errorf("attrs changed: %v", attrs)
	}
}

func BenchmarkHandle(b *testing.B) {
	file := redactSchema(b)
	secret := newMessage(b, file, "Secret", map[protoreflect.Name]string{"token": "tok", "name": "n"})
	plain := newMessage(b, file, "Plain", map[protoreflect.Name]string{"name": "n"})
	logger := slog.New(NewHandler(slog.DiscardHandler))
	ctx := context.Background()

	b.Run("no proto", func(b *testing.B) {
		for b.Loop() {
			logger.InfoContext(ctx, "m", slog.String("method", "/x/Y"), slog.Int("n", 1), slog.String("elapsed", "1ms"))
		}
	})
	b.Run("proto without redacted fields", func(b *testing.B) {
		for b.Loop() {
			logger.InfoContext(ctx, "m", slog.String("method", "/x/Y"), slog.Any("req", plain), slog.Any("resp", plain))
		}
	})
	b.Run("proto with redacted fields", func(b *testing.B) {
		for b.Loop() {
			logger.InfoContext(ctx, "m", slog.String("method", "/x/Y"), slog.Any("req", secret), slog.Any("resp", plain))
		}
	})
}

func TestWithAttrsRedactsProto(t *testing.T) {
	file := redactSchema(t)
	secret := newMessage(t, file, "Secret", map[protoreflect.Name]string{"token": "tok"})

	rec := &recordingHandler{}
	h := NewHandler(rec).WithAttrs([]slog.Attr{slog.Any("bound", secret)})
	inner := h.(*ContextHandler).internal.(*recordingHandler)
	if len(inner.attrs) != 1 {
		t.Fatalf("inner handler got %d attrs, want 1", len(inner.attrs))
	}
	got, ok := inner.attrs[0].Value.Any().(proto.Message)
	if !ok {
		t.Fatalf("bound attr is %T, want proto.Message", inner.attrs[0].Value.Any())
	}
	if v := stringField(got, "token"); v != "[REDACTED]" {
		t.Errorf("bound.token = %q, want %q", v, "[REDACTED]")
	}
}
