# connectivity-go-kit

Shared plumbing for Connectivity's Go services. It holds only what every service does the same way. Business logic stays in the services.

Part of [Connectivity](https://github.com/shubhampatel19/connectivity-platform), a mock edge-to-cloud telemetry system built for practicing production debugging.

```go
import "github.com/shubhampatel19/connectivity-go-kit/platform"
```

| What | Why it's shared |
|---|---|
| `platform.Env` | Typed config from environment variables that reports every invalid setting at once |
| `platform.NewLogger` | JSON `slog` logger tagged with the service name, with the level set by `LOG_LEVEL` |
| `platform.Serve` | HTTP server with sane timeouts and graceful drain on shutdown |
| `platform.NewRegistry` / `MetricsHandler` | Prometheus registry preloaded with Go runtime and process collectors |
| `platform.InitTracing` | OpenTelemetry: W3C trace context always, OTLP/HTTP span export when `OTEL_EXPORTER_OTLP_ENDPOINT` is set |
| `platform.TraceHTTP` | Server span per route, continuing the caller's trace from its `traceparent` header |
| `platform.TraceHeaders` / `ContextFromHeaders` | Trace context as key/value pairs, for Kafka record headers |
| `platform.InjectHTTP`, `RecordError`, `TraceID` | Outgoing requests, failed spans, and the trace ID for responses or logs |

## Tracing

Every service calls `InitTracing` at startup. It always installs the W3C trace-context propagator, so a trace that starts on a device carries on through HTTP headers, the site gateway's disk queue and Kafka record headers. Spans are only exported when `OTEL_EXPORTER_OTLP_ENDPOINT` is set (cluster mode points it at Tempo); otherwise nothing is recorded, but context still passes through, so a service that doesn't export never breaks a trace.

The standard OpenTelemetry variables apply: `OTEL_TRACES_SAMPLER=parentbased_traceidratio` with `OTEL_TRACES_SAMPLER_ARG=0.1` samples 10% of new traces and follows the caller's decision for the rest; `OTEL_SERVICE_NAME` overrides the service name.

Log lines written with a context (`log.InfoContext(ctx, ...)`) inside a sampled trace get `trace_id` and `span_id` fields. Loki keeps `trace_id` as structured metadata, and Grafana links it to the trace in Tempo.

OpenTelemetry is pinned at v1.34.0, the last release that supports Go 1.22, which every module here still targets.
