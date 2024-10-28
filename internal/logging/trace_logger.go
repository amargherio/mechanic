package logging

import (
	"context"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type TraceCore struct {
	zapcore.Core
	Ctx *context.Context
	tp  trace.TracerProvider
}

// NewTraceCore Returns a new Core that adds tracing information to the log entry
func NewTraceCore(c zapcore.Core, ctx *context.Context, tp trace.TracerProvider) *TraceCore {
	return &TraceCore{c, ctx, tp}
}

// With adds structured context to the Core
func (c *TraceCore) With(fields []zapcore.Field) zapcore.Core {
	return &TraceCore{
		Core: c.Core.With(fields),
		Ctx:  c.Ctx,
		tp:   c.tp,
	}
}

func (c *TraceCore) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if ce := c.Core.Check(e, ce); ce != nil {

		return ce.AddCore(e, c)
	}
	return ce
}

// Write serializes the Entry and any Fields, along with the trace information, to the Core
func (c *TraceCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// Get the current span from the context, if there is one
	if span := trace.SpanFromContext(*c.Ctx); span != nil {
		spanContext := span.SpanContext()
		if spanContext.IsValid() {
			fields = append(
				fields,
				zap.String("traceID", spanContext.TraceID().String()),
				zap.String("spanID", spanContext.SpanID().String()),
			)

			if entry.Level == zapcore.DebugLevel {
				fields = append(fields, zap.Any("spanContext", spanContext))
			}
		}
	}
	return c.Core.Write(entry, fields)
}

func (c *TraceCore) Sync() error {
	return c.Core.Sync()
}
