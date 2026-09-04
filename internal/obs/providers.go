package obs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Shutdown flushes and stops every provider. Call it on the way out: a batch
// span processor holds finished spans in memory, so a process that exits
// without flushing loses whatever had not hit the batch timeout yet -- which,
// on a short-lived request, is usually the span you wanted.
type Shutdown func(context.Context) error

// Setup builds the tracer and meter providers, registers them globally, and
// installs the W3C propagators.
//
// Exemplars need no configuration here, and that is worth stating plainly
// because the opposite is widely assumed. The Go metric SDK's default exemplar
// filter is TraceBasedFilter, which records an exemplar whenever a measurement
// is taken with a context carrying a SAMPLED span. There is no "enable
// exemplars" switch to turn on. What there is instead are two ways to silently
// get none:
//
//   - recording a measurement with a context that has no span in it, or
//   - sampling the span away.
//
// Both fail quietly. Neither logs anything. See README.md, "Exemplars".
func Setup(ctx context.Context, c Config) (Shutdown, error) {
	res, err := newResource(ctx, c)
	if err != nil {
		return nil, err
	}

	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(c.OTLPEndpoint),
		otlptracegrpc.WithInsecure(), // local stack only; TLS in any real deployment
	)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}

	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(c.OTLPEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp metric exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExp),
		// AlwaysSample is right for a demonstration and wrong for production.
		// It is also load-bearing here: TraceBasedFilter only records an
		// exemplar for a SAMPLED span, so a head sampler that drops 99% of
		// traces drops 99% of exemplars with them. If you lower this, expect
		// the exemplar dots on the latency panel to thin out to nothing.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			// Short interval so the dashboard fills in while someone is
			// watching it. Production would leave this at the 60s default.
			sdkmetric.WithInterval(5*time.Second),
		)),
		// No exemplar option is set on purpose. The default filter is already
		// TraceBasedFilter; naming it here would imply it needed turning on.
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	// W3C traceparent/tracestate plus baggage. Without this the outbound call
	// to the downstream carries no correlation header and the trace ends at
	// this service's own span.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}
