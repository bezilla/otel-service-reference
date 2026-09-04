// Package api is the HTTP surface of the service. The business logic is
// deliberately trivial -- the instrumentation is the product here.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/bezilla/otel-service-reference/internal/downstream"
	"github.com/bezilla/otel-service-reference/internal/faults"
	"github.com/bezilla/otel-service-reference/internal/obs"
)

// Server holds the handler dependencies.
type Server struct {
	Cfg      obs.Config
	Log      *slog.Logger
	Pricing  *downstream.Client
	Injector *faults.Injector
}

// tracer is obtained from the global provider. Instrumentation code takes the
// API, never the SDK: that is what lets a test swap in a recorder and what
// keeps exporter choice in one package.
func tracer() trace.Tracer {
	return otel.Tracer("github.com/bezilla/otel-service-reference/internal/api")
}

// identityMiddleware copies service.name and service.namespace onto the metric
// data points that otelhttp emits.
//
// otelhttp's Labeler is the supported way to add attributes to instrumentation
// metrics: the handler appends labeler.Get() to the metric attributes for the
// request. The deprecated WithMetricAttributesFn does the same thing and is on
// its way out.
//
// Why this is needed at all is the single least obvious thing in this
// repository, and it is explained in obs.ResourceAttributes and in the README:
// resource attributes do not become Prometheus labels, so a platform recording
// rule that aggregates by (service_name, service_namespace) sees nothing unless
// these ride on the data point.
func (s *Server) identityMiddleware(next http.Handler) http.Handler {
	attrs := s.Cfg.MetricIdentityAttributes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
			labeler.Add(attrs...)
		}
		next.ServeHTTP(w, r)
	})
}

// Routes builds the API mux, wrapped in otelhttp.
//
// otelhttp gives the HTTP-layer span and the http.server.* metrics for free.
// What it cannot give is a span around the part of the request that is specific
// to this service, because it does not know what that is. That is the division
// of labour between automatic and manual instrumentation, and both handlers
// below show it.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/quote", s.handleQuote)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /admin/inject", s.handleInject)
	mux.HandleFunc("GET /admin/inject", s.handleInjectGet)

	// The order matters: identityMiddleware must run INSIDE otelhttp, because
	// the Labeler it writes to is put into the context by otelhttp's handler.
	// Wrapping the other way round would add the attributes to a Labeler that
	// nothing ever reads, and the labels would silently not appear.
	instrumented := otelhttp.NewHandler(s.identityMiddleware(mux), "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool {
			// Health checks would otherwise dominate both the trace volume and
			// the request-rate panel with traffic nobody asked about.
			return r.URL.Path != "/healthz"
		}),
	)
	return instrumented
}

type quoteResponse struct {
	SKU      string  `json:"sku"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	Discount float64 `json:"discount"`
	Final    float64 `json:"final"`
}

// handleQuote is the endpoint worth tracing: it calls a dependency and then
// does work of its own.
func (s *Server) handleQuote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sku := r.URL.Query().Get("sku")
	if sku == "" {
		sku = "SKU-1"
	}

	q, err := s.Pricing.Price(ctx, sku)
	if err != nil {
		// RecordError puts the error on the span; SetStatus is what makes the
		// span show as failed in Jaeger. Doing only the first leaves a span
		// that carries the error but still reads as successful.
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		span.SetStatus(codes.Error, "pricing lookup failed")
		s.Log.ErrorContext(ctx, "pricing lookup failed", slog.String("sku", sku), slog.Any("error", err))
		http.Error(w, "upstream unavailable", http.StatusInternalServerError)
		return
	}

	discount := s.applyDiscount(ctx, sku, q.Price)

	resp := quoteResponse{
		SKU: q.SKU, Price: q.Price, Currency: q.Currency,
		Discount: discount, Final: q.Price - discount,
	}
	s.Log.InfoContext(ctx, "quote served",
		slog.String("sku", sku), slog.Float64("final", resp.Final))

	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// applyDiscount is the hand-rolled span the README talks about.
//
// otelhttp already produced a span covering this whole request, so why another
// one? Because the automatic span can only say "GET /api/quote took 430ms". It
// cannot say which 430ms. When the dependency call and this calculation are
// both inside one span, a latency regression here and a latency regression in
// the dependency look identical on a trace waterfall. Splitting them out is the
// difference between knowing a request was slow and knowing what was slow.
//
// The rule of thumb: automatic instrumentation covers the boundaries a
// framework knows about; you add spans around the decisions your service makes.
func (s *Server) applyDiscount(ctx context.Context, sku string, price float64) float64 {
	_, span := tracer().Start(ctx, "pricing.apply_discount",
		trace.WithAttributes(
			attribute.String("pricing.sku", sku),
			attribute.Float64("pricing.list_price", price),
		),
	)
	defer span.End()

	// Stand-in for whatever the service actually does with the number.
	time.Sleep(2 * time.Millisecond)
	discount := 0.0
	if price > 10 {
		discount = price * 0.1
	}

	span.SetAttributes(attribute.Float64("pricing.discount", discount))
	return discount
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleInjectGet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Injector.Get())
}

// handleInject tunes the fault injection at runtime, so a demo can change the
// shape of the telemetry without a restart.
func (s *Server) handleInject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseLatencyMS *int     `json:"base_latency_ms"`
		TailLatencyMS *int     `json:"tail_latency_ms"`
		TailPercent   *float64 `json:"tail_percent"`
		ErrorRate     *float64 `json:"error_rate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	c := s.Injector.Get()
	if body.BaseLatencyMS != nil {
		c.BaseLatency = time.Duration(*body.BaseLatencyMS) * time.Millisecond
	}
	if body.TailLatencyMS != nil {
		c.TailLatency = time.Duration(*body.TailLatencyMS) * time.Millisecond
	}
	if body.TailPercent != nil {
		c.TailPercent = *body.TailPercent
	}
	if body.ErrorRate != nil {
		c.ErrorRate = *body.ErrorRate
	}
	s.Injector.Set(c)

	s.Log.InfoContext(r.Context(), "injection updated",
		slog.Float64("error_rate", c.ErrorRate),
		slog.Float64("tail_percent", c.TailPercent))

	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}
