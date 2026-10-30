package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordSpans installs an in-memory tracer provider for one test.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return rec
}

const parentTrace = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestTraceHTTPContinuesTheCallersTrace(t *testing.T) {
	rec := recordSpans(t)
	var inner trace.SpanContext
	h := TraceHTTP("POST /v1/telemetry", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry", nil)
	req.Header.Set("traceparent", "00-"+parentTrace+"-00f067aa0ba902b7-01")
	h.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name() != "POST /v1/telemetry" || s.SpanKind() != trace.SpanKindServer {
		t.Errorf("span %q kind %v", s.Name(), s.SpanKind())
	}
	if got := s.SpanContext().TraceID().String(); got != parentTrace {
		t.Errorf("trace %s, want the caller's %s", got, parentTrace)
	}
	if got := s.Parent().SpanID().String(); got != "00f067aa0ba902b7" {
		t.Errorf("parent span %s", got)
	}
	if s.Status().Code != codes.Error {
		t.Errorf("a 503 should mark the span as an error, got %v", s.Status())
	}
	if inner.SpanID() != s.SpanContext().SpanID() {
		t.Error("the handler did not run inside the server span")
	}
}

func TestTraceHTTPLeavesClientErrorsAlone(t *testing.T) {
	rec := recordSpans(t)
	h := TraceHTTP("POST /v1/alerts", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/alerts", nil))
	if s := rec.Ended()[0]; s.Status().Code == codes.Error || s.Parent().IsValid() {
		t.Errorf("a 400 with no traceparent: status %v, parent valid %v", s.Status(), s.Parent().IsValid())
	}
}

func TestTraceHeadersRoundTrip(t *testing.T) {
	recordSpans(t)
	ctx, span := Tracer().Start(context.Background(), "produce")
	defer span.End()

	headers := TraceHeaders(ctx)
	if headers["traceparent"] == "" {
		t.Fatalf("no traceparent in %v", headers)
	}
	got := trace.SpanContextFromContext(ContextFromHeaders(context.Background(), headers))
	if got.TraceID() != span.SpanContext().TraceID() || got.SpanID() != span.SpanContext().SpanID() || !got.IsRemote() {
		t.Errorf("round trip lost the span: %v", got)
	}
	if len(TraceHeaders(context.Background())) != 0 {
		t.Error("no trace in the context should mean no headers")
	}
}

func TestContextPassesThroughWithoutExport(t *testing.T) {
	// The default (no-op) provider records nothing, but a trace that came in
	// must still go out, or a non-exporting hop would break the trace.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	in := map[string]string{"traceparent": "00-" + parentTrace + "-00f067aa0ba902b7-01"}
	ctx, span := Tracer().Start(ContextFromHeaders(context.Background(), in), "hop")
	defer span.End()
	if got := TraceHeaders(ctx)["traceparent"]; got == "" || got[3:35] != parentTrace {
		t.Errorf("trace context was not passed on: %q", got)
	}
}

func TestLogLinesCarryTheSampledTrace(t *testing.T) {
	recordSpans(t)
	var buf bytes.Buffer
	log := slog.New(traceHandler{slog.NewJSONHandler(&buf, nil)}).With("service", "test")

	ctx, span := Tracer().Start(context.Background(), "work")
	log.InfoContext(ctx, "inside")
	span.End()
	log.Info("outside")

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	var inside, outside map[string]any
	_ = json.Unmarshal(lines[0], &inside)
	_ = json.Unmarshal(lines[1], &outside)
	if inside["trace_id"] != span.SpanContext().TraceID().String() || inside["span_id"] == nil {
		t.Errorf("line logged inside a span: %v", inside)
	}
	if _, ok := outside["trace_id"]; ok {
		t.Errorf("line logged without a span has a trace_id: %v", outside)
	}
	if TraceID(ctx) != span.SpanContext().TraceID().String() || TraceID(context.Background()) != "" {
		t.Error("TraceID disagrees with the span")
	}
}
