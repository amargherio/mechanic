package tracing

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"io"
)

func InitTracer() (trace.TracerProvider, error) {
	// configure the stdout exporter
	// todo: add support for additional exporters
	options := stdouttrace.WithWriter(io.Discard)
	exporter, err := stdouttrace.New(options)
	//exporter, err := stdouttrace.New()
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
