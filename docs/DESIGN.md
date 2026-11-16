# connectivity-go-kit: design

One package, `platform`, holding what every Go service in Connectivity does identically: read configuration, log, serve HTTP, expose metrics, trace. Nothing in it knows what a device or a case is.

## The rule for what belongs here

Code goes here when two things are true: every service needs it, and services behaving differently would hurt during an incident. Log format is the clearest case. If each service logged its own way, one Loki query couldn't cover them and `trace_id` wouldn't link anywhere. Business logic, clients for Kafka or Postgres, and anything only one service uses stay in the services.

The cost of a shared module is coupling: a change here is a release that every service must pick up. So the package stays small and its API changes rarely.

## Files

| File | What it gives a service |
|---|---|
| `env.go` | `Env`: typed settings from environment variables |
| `log.go` | `NewLogger`: JSON logs with the service name, level from `LOG_LEVEL`, and trace IDs |
| `serve.go` | `Serve`: an HTTP server with timeouts and graceful shutdown; `NewRegistry`, `MetricsHandler` |
| `tracing.go` | `InitTracing`, `TraceHTTP`, and helpers to carry trace context over HTTP and Kafka |

## Configuration: report everything at once

`Env` reads each variable, converts it, and remembers every failure instead of stopping at the first. `Err()` returns them all together.

```go
var env platform.Env
addr := env.String("HTTP_ADDR", ":8080")
secret := env.Required("TOKEN_SECRET")
timeout := env.Duration("PRODUCE_TIMEOUT", 5*time.Second)
env.Check(timeout > 0, "PRODUCE_TIMEOUT must be positive")
if err := env.Err(); err != nil { return err }
```

A service with three bad settings should say so in one start, not across three crash loops. A bad setting stops the service at startup on purpose: a service that starts with a default it didn't mean is harder to find than one that refuses to start.

## Logging

`NewLogger` returns an `slog` JSON logger. Each line has `service`, and, when written with a context inside a sampled trace (`log.InfoContext(ctx, ...)`), `trace_id` and `span_id`. That pair is what lets Grafana jump from a log line to its trace and back.

JSON is used because logs are read by Loki queries far more often than by eye, and `| json | level="ERROR"` needs fields, not prose.

## Serving HTTP

`Serve` sets four timeouts, because Go's defaults are none at all, and a server with no timeouts can be held open by a client that sends one byte a minute:

| Timeout | Value | Protects against |
|---|---|---|
| `ReadHeaderTimeout` | 5s | A client that opens a connection and never finishes its headers |
| `ReadTimeout` | 15s | A body that trickles in |
| `WriteTimeout` | 15s | A client that never reads the response |
| `IdleTimeout` | 90s | Keep-alive connections held forever |

These suit small JSON requests. A handler that legitimately needs longer (the ingest API's bundle chunks) extends the deadline for its own request with `http.ResponseController`; the response writers this package wraps implement `Unwrap` so that works.

On shutdown, `Serve` stops accepting connections and gives requests in flight ten seconds. That, plus a `preStop` sleep in Kubernetes, is why a rolling deploy costs devices nothing.

## Metrics

`NewRegistry` returns a registry that already has the Go runtime and process collectors: goroutines, heap, GC pauses, open file descriptors. Those are the first graphs to look at for a leak, so every service has them without asking. Services register their own metrics on the same registry.

A dedicated registry is used instead of the global default so tests can create as many as they like without collisions.

## Tracing

`InitTracing` always installs the W3C trace-context propagator and only exports spans when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. The split matters: a service that isn't exporting still passes `traceparent` along, so it never breaks a trace that started upstream.

| Helper | Used for |
|---|---|
| `TraceHTTP(route, handler)` | A server span per route, continuing the caller's trace; marks 5xx as errors |
| `InjectHTTP(ctx, header)` | Outgoing HTTP requests |
| `TraceHeaders(ctx)` / `ContextFromHeaders(ctx, map)` | Kafka record headers, in and out |
| `RecordError(ctx, err)` | Marking the current span failed |
| `TraceID(ctx)` | The `X-Trace-ID` response header |

Sampling is decided once, at the device (`parentbased_traceidratio`), and every later service follows that decision. A trace is therefore either complete or absent, never a fragment.

OpenTelemetry is pinned at v1.34.0, the last release supporting Go 1.22.

## Releasing

Services pin this module by commit. After a change is merged: in each service, `go get github.com/shubhampatel19/connectivity-go-kit@<commit>`, commit `go.mod` and `go.sum`, and let CI run. There is no need to update every service at once; an old pin keeps working.
