package obs

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

// traceHandler is a slog.Handler that stamps trace_id and span_id onto every
// record written inside an active span.
//
// This is the whole of the log-to-trace pivot. Grafana's Loki data source can
// turn a field named trace_id into a link to the trace backend, and Jaeger can
// be searched by the same value by hand. The field names matter: trace_id and
// span_id, lower snake case, matching the labels the collector's remote-write
// translator attaches to exemplars. Using traceID here would work for humans
// and break every automatic pivot.
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(as)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}

// NewLogger returns a JSON logger that correlates with traces.
//
// Logs go to stdout as JSON rather than over OTLP. That is a deliberate choice
// for a reference service: stdout is what a container runtime already collects,
// it needs no exporter to be running for the service to be debuggable, and it
// keeps the log path independent of the telemetry path. The correlation that
// matters -- trace_id on the line -- does not require the logs themselves to
// travel over OTLP.
func NewLogger(c Config) *slog.Logger {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(traceHandler{base}).With(
		slog.String("service_name", c.ServiceName),
		slog.String("service_namespace", c.ServiceNamespace),
	)
}
