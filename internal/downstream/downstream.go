// Package downstream is the simulated dependency and the client that calls it.
//
// It is a real HTTP hop on purpose. A fake dependency implemented as a function
// call would produce a child span too, but it would not exercise the part that
// actually breaks in production: serialising a trace context into a W3C
// traceparent header, putting it on the wire, and reading it back out on the
// other side. If propagation is broken you get two disconnected single-span
// traces, and the only way to see that is to make the hop real.
package downstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/bezilla/otel-service-reference/internal/faults"
)

// Quote is what the dependency returns.
type Quote struct {
	SKU      string  `json:"sku"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
}

// Client calls the pricing dependency over HTTP.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient wraps the transport with otelhttp. That wrapper does two things at
// once, and both matter: it creates the CLIENT span, and it injects the
// traceparent header using the globally registered propagator. Using a bare
// http.Client here would still produce a working service and a silently broken
// trace.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout:   5 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// Price fetches a quote for a SKU.
func (c *Client) Price(ctx context.Context, sku string) (Quote, error) {
	url := fmt.Sprintf("%s/pricing?sku=%s", c.baseURL, sku)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Quote{}, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Quote{}, fmt.Errorf("call pricing: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("pricing returned %s", resp.Status)
	}

	var q Quote
	if err := json.NewDecoder(resp.Body).Decode(&q); err != nil {
		return Quote{}, fmt.Errorf("decode quote: %w", err)
	}
	return q, nil
}

// Handler is the dependency itself: a second HTTP service, wrapped in otelhttp
// by the caller, that spends time and sometimes fails.
func Handler(inj *faults.Injector) http.Handler {
	tracer := otel.Tracer("github.com/bezilla/otel-service-reference/internal/downstream")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sku := r.URL.Query().Get("sku")

		// A hand-rolled span inside the dependency, so the trace shows where the
		// time actually went rather than just that the call was slow.
		ctx, span := tracer.Start(ctx, "pricing.lookup",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attribute.String("pricing.sku", sku)),
		)
		defer span.End()

		delay, slow := inj.Latency()
		span.SetAttributes(attribute.Bool("pricing.slow_path", slow))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			span.SetStatus(codes.Error, "client went away")
			http.Error(w, "cancelled", http.StatusRequestTimeout)
			return
		}

		if inj.ShouldFail() {
			span.SetStatus(codes.Error, "injected dependency failure")
			http.Error(w, "pricing unavailable", http.StatusInternalServerError)
			return
		}

		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(Quote{SKU: sku, Price: 12.5, Currency: "USD"})
	})
}
