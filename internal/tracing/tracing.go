package tracing

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func InitTracer(shouldTrace bool) (trace.TracerProvider, error) {
	// exit early with no-op if tracing isn't enabled
	if !shouldTrace {
		tp := noop.NewTracerProvider()
		otel.SetTracerProvider(tp)
		return tp, nil
	}

	// configure the stdout exporter
	// todo: add support for additional exporters
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.Default()),
	)

	otel.SetTracerProvider(tp)
	return tp, nil
}
