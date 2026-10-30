package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Tracing with OpenTelemetry.
//
// Every service installs the W3C trace-context propagator, so a trace that
// starts on a device survives every hop: HTTP headers, the gateway's disk
// queue, Kafka record headers. Spans are exported over OTLP/HTTP only when
// OTEL_EXPORTER_OTLP_ENDPOINT (or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) is set.
// Without it nothing is recorded, but trace context still passes through
// untouched, so a service that doesn't export can't break a trace.
//
// The standard OTEL_* variables configure the rest: OTEL_TRACES_SAMPLER and
// OTEL_TRACES_SAMPLER_ARG for sampling (the default samples everything a
// parent didn't decide against), OTEL_SERVICE_NAME and
// OTEL_RESOURCE_ATTRIBUTES for the resource.

const instrumentationName = "github.com/shubhampatel19/connectivity-go-kit/platform"

// TracingEnabled reports whether spans will be exported.
func TracingEnabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// InitTracing sets up trace propagation and, when an OTLP endpoint is
// configured, span export. Call the returned function on shutdown to flush
// spans still in memory; it is safe to call when tracing is off.
func InitTracing(ctx context.Context, service string, log *slog.Logger) (shutdown func(context.Context) error, err error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if !TracingEnabled() {
		log.Info("tracing: propagating trace context only; set OTEL_EXPORTER_OTLP_ENDPOINT to export spans")
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	// Attributes from the environment come last, so OTEL_SERVICE_NAME wins.
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(service)),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithFromEnv(),
	)
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("build trace resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	// Export failures (Tempo down, say) are logged through the service's
	// logger instead of the SDK's plain-text default.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn("tracing: export failed", "err", err)
	}))
	log.Info("tracing: exporting spans over OTLP/HTTP")
	return tp.Shutdown, nil
}

// Tracer is the tracer every service uses for its own spans.
func Tracer() trace.Tracer { return otel.Tracer(instrumentationName) }

// TraceHTTP wraps one route's handler in a server span named after the route
// (e.g. "POST /v1/telemetry"), continuing the caller's trace when the
// request carries a traceparent header. 5xx responses mark the span as an
// error; 4xx don't, because the server did its job.
func TraceHTTP(route string, next http.Handler) http.Handler {
	routeAttr := route
	if _, path, ok := strings.Cut(route, " "); ok {
		routeAttr = path
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := Tracer().Start(ctx, route,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(r.Method),
				semconv.HTTPRoute(routeAttr),
				semconv.URLPath(r.URL.Path),
			))
		defer span.End()

		rec := &spanStatusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))
		span.SetAttributes(semconv.HTTPResponseStatusCode(rec.status))
		if rec.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}
	})
}

type spanStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *spanStatusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *spanStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// InjectHTTP writes the trace context in ctx into outgoing request headers.
func InjectHTTP(ctx context.Context, h http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(h))
}

// TraceHeaders returns the trace context in ctx as key/value pairs, for
// carriers that aren't HTTP requests, such as Kafka record headers. It is
// empty when there is no trace to continue.
func TraceHeaders(ctx context.Context) map[string]string {
	m := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, m)
	return m
}

// ContextFromHeaders continues the trace carried in headers produced by
// TraceHeaders (or by any W3C trace-context propagator).
func ContextFromHeaders(ctx context.Context, headers map[string]string) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(headers))
}

// RecordError marks the span in ctx as failed with err.
func RecordError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// TraceID returns the ID of the sampled trace in ctx, or "" when there is
// none. Logs only link to traces that were sampled, so a trace_id in a log
// line always opens something in Tempo.
func TraceID(ctx context.Context) string {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() && sc.IsSampled() {
		return sc.TraceID().String()
	}
	return ""
}
