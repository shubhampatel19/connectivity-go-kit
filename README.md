# connectivity-go-kit

Shared plumbing for Connectivity's Go services. It holds only what every service does the same way. Business logic stays in the services.

Part of [Connectivity](https://github.com/spr732/connectivity-platform), a mock edge-to-cloud telemetry system built for practicing production debugging.

```go
import "github.com/spr732/connectivity-go-kit/platform"
```

| What | Why it's shared |
|---|---|
| `platform.Env` | Typed config from environment variables that reports every invalid setting at once |
| `platform.NewLogger` | JSON `slog` logger tagged with the service name, with the level set by `LOG_LEVEL` |
| `platform.Serve` | HTTP server with sane timeouts and graceful drain on shutdown |
| `platform.NewRegistry` / `MetricsHandler` | Prometheus registry preloaded with Go runtime and process collectors |
