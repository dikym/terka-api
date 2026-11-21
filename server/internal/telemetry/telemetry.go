// Package telemetry wires OpenTelemetry traces and metrics to an OTLP collector. Without an
// endpoint it installs nothing, so the instrumentation calls elsewhere stay no-ops.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

type Options struct {
	Endpoint, Service, Version, Env string
}

func Resource(o Options) *resource.Resource {
	return resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(o.Service),
		semconv.ServiceVersion(o.Version),
		semconv.DeploymentEnvironmentName(o.Env),
	)
}

// Setup installs the global tracer and meter providers. It never sets a propagator: a traceparent
// header from the internet must not decide which trace a request joins.
func Setup(ctx context.Context, o Options, log *slog.Logger) (func(context.Context) error, error) {
	if o.Endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	traceExp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(o.Endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	metricExp, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(o.Endpoint), otlpmetricgrpc.WithInsecure())
	if err != nil {
		return nil, errors.Join(err, traceExp.Shutdown(ctx))
	}

	res := Resource(o)
	tp := NewTracerProvider(res, sdktrace.WithBatcher(traceExp))
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(30*time.Second))),
	)

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn("telemetry export failed", "error", err)
	}))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}

// NewTracerProvider always redacts url.query, which can carry OAuth codes and email tokens.
func NewTracerProvider(res *resource.Resource, opts ...sdktrace.TracerProviderOption) *sdktrace.TracerProvider {
	all := []sdktrace.TracerProviderOption{sdktrace.WithSpanProcessor(redactor{})}
	if res != nil {
		all = append(all, sdktrace.WithResource(res))
	}
	return sdktrace.NewTracerProvider(append(all, opts...)...)
}

type redactor struct{}

func (redactor) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	for _, kv := range s.Attributes() {
		if kv.Key == "url.query" {
			s.SetAttributes(attribute.String("url.query", "redacted"))
			return
		}
	}
}

func (redactor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (redactor) Shutdown(context.Context) error   { return nil }
func (redactor) ForceFlush(context.Context) error { return nil }
