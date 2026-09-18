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
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	"github.com/agent-substrate/substrate/internal/ateattr"
	"github.com/agent-substrate/substrate/internal/protoredact"
)

// ContextHandler adds trace correlation to records and redacts proto message
// attribute values (see internal/protoredact) before delegating.
type ContextHandler struct {
	internal slog.Handler
}

func NewHandler(internal slog.Handler) *ContextHandler {
	return &ContextHandler{
		internal: internal,
	}
}

func (h *ContextHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.internal.Enabled(ctx, lvl)
}

// Handle records the active span on the log record under the field names the OTel
// spec fixes for non-OTLP log formats, so a collector can lift them onto the log
// record's own trace fields. Gated on the whole span context being valid: a trace
// ID without a span ID names a request but not the operation within it.
func (h *ContextHandler) Handle(ctx context.Context, rec slog.Record) error {
	rec = redactRecord(rec)

	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		rec.AddAttrs(
			slog.String(ateattr.LogTraceIDField, sc.TraceID().String()),
			slog.String(ateattr.LogSpanIDField, sc.SpanID().String()),
			slog.String(ateattr.LogTraceFlagsField, fmt.Sprintf("%02x", byte(sc.TraceFlags()))),
		)
	}

	return h.internal.Handle(ctx, rec)
}

func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if redacted, changed := redactAttrs(attrs); changed {
		attrs = redacted
	}
	return &ContextHandler{internal: h.internal.WithAttrs(attrs)}
}

// redactRecord rebuilds rec with its proto attribute values redacted, or
// returns rec unchanged if none need it.
func redactRecord(rec slog.Record) slog.Record {
	needs := false
	rec.Attrs(func(a slog.Attr) bool {
		needs = needsRedaction(a.Value)
		return !needs
	})
	if !needs {
		return rec
	}
	attrs := make([]slog.Attr, 0, rec.NumAttrs())
	rec.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	redacted, _ := redactAttrs(attrs)
	out := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	out.AddAttrs(redacted...)
	return out
}

func needsRedaction(v slog.Value) bool {
	switch v.Kind() {
	case slog.KindAny:
		m, ok := v.Any().(proto.Message)
		if !ok {
			return false
		}
		rm := m.ProtoReflect()
		return rm.IsValid() && protoredact.HasRedactedFields(rm.Descriptor())
	case slog.KindGroup:
		for _, a := range v.Group() {
			if needsRedaction(a.Value) {
				return true
			}
		}
	}
	return false
}

// redactAttrs returns a redacted copy of attrs and whether anything changed.
func redactAttrs(attrs []slog.Attr) ([]slog.Attr, bool) {
	var out []slog.Attr
	for i, a := range attrs {
		v, changed := redactValue(a.Value)
		if !changed {
			continue
		}
		if out == nil {
			out = make([]slog.Attr, len(attrs))
			copy(out, attrs)
		}
		out[i] = slog.Attr{Key: a.Key, Value: v}
	}
	return out, out != nil
}

func redactValue(v slog.Value) (slog.Value, bool) {
	switch v.Kind() {
	case slog.KindAny:
		m, ok := v.Any().(proto.Message)
		if !ok {
			return v, false
		}
		r := protoredact.Redact(m)
		if r == m {
			return v, false
		}
		return slog.AnyValue(r), true
	case slog.KindGroup:
		group, changed := redactAttrs(v.Group())
		if !changed {
			return v, false
		}
		return slog.GroupValue(group...), true
	default:
		return v, false
	}
}

func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{internal: h.internal.WithGroup(name)}
}
