package logging

import (
	"context"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type TraceCore struct {
	ioCore zapcore.Core
	Ctx    *context.Context
	tp     trace.TracerProvider
}

// NewTraceCore Returns a new Core that adds tracing information to the log entry
func NewTraceCore(c zapcore.Core, ctx *context.Context, tp trace.TracerProvider) *TraceCore {
	return &TraceCore{c, ctx, tp}
}

func (c *TraceCore) Enabled(lvl zapcore.Level) bool {
	return c.ioCore.Enabled(lvl)
}

// With adds structured context to the Core
func (c *TraceCore) With(fields []zapcore.Field) zapcore.Core {
	return c.ioCore.With(fields)
}

func (c *TraceCore) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(e.Level) {
		return ce.AddCore(e, c)
	}
	return ce
}

// Write serializes the Entry and any Fields, along with the trace information, to the Core
func (c *TraceCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// 1. get the supplied context from the fields (or the span if the context doesn't cut it).
	// 2. if context, get the span from the context
	// 3. using the span, add the trace ID, span ID, and name to the logged fields
	// 4. write the entry to the core

	// Get the current span from the context, if there is one
	var sc context.Context
	var activeSpan trace.Span
	for _, field := range fields {
		if field.Key == "traceCtx" {
			sc = field.Interface.(context.Context)
			break
		}
	}
	if sc == nil {
		return c.ioCore.Write(entry, fields)
	}
	activeSpan = trace.SpanFromContext(sc)

	// if we still didn't get an active span, skip those extra fields and write the entry
	// todo: should we also check if the active span is recording here?
	if activeSpan != nil && activeSpan.SpanContext().IsValid() {
		ros := activeSpan.(sdktrace.ReadOnlySpan)
		fields = append(
			fields,
			zap.String("traceID", ros.SpanContext().TraceID().String()),
			zap.String("spanID", ros.SpanContext().SpanID().String()),
			zap.String("spanName", ros.Name()),
		)

		if entry.Level == zapcore.DebugLevel {
			fields = append(fields,
				zap.Any("spanContext", ros.SpanContext()),
				zap.Any("spanAttributes", ros.Attributes()),
				zap.Any("rawSpan", ros),
			)
		}
	}
	return c.ioCore.Write(entry, fields)
}

func (c *TraceCore) Sync() error {
	return c.ioCore.Sync()
}
