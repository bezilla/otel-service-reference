package obs_test

import (
	"context"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"

	"github.com/bezilla/otel-service-reference/internal/api"
	"github.com/bezilla/otel-service-reference/internal/downstream"
	"github.com/bezilla/otel-service-reference/internal/faults"
	"github.com/bezilla/otel-service-reference/internal/obs"
)

// This file is the only test that proves telemetry LEAVES the process.
//
// internal/api's tests assert against a ManualReader, which reads the SDK's
// in-memory state. That is the right tool for the instrumentation mistakes --
// a measurement recorded without a span in context, an identity attribute left
// off a data point -- and it is what those tests are for. What it cannot see is
// anything about export, because with a ManualReader nothing is exported. An
// endpoint that never dials, a resource that never reaches the wire, a shutdown
// that returns before the batch processor has flushed: every one of those
// passes a ManualReader test and produces an empty panel.
//
// So the receiver below speaks the real protocol over a real socket, and every
// assertion is made on the bytes an OTLP collector would actually have
// received. It runs in-process and needs no container, because the thing worth
// proving is what this service put on the wire, not what a collector does next.

// captured holds what the receiver was sent. Guarded because gRPC serves each
// export on its own goroutine.
type captured struct {
	mu      sync.Mutex
	spans   []*tracepb.ResourceSpans
	metrics []*metricspb.ResourceMetrics
}

func (c *captured) addSpans(rs []*tracepb.ResourceSpans) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spans = append(c.spans, rs...)
}

func (c *captured) addMetrics(rm []*metricspb.ResourceMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics = append(c.metrics, rm...)
}

func (c *captured) snapshot() ([]*tracepb.ResourceSpans, []*metricspb.ResourceMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.spans, c.metrics
}

// The two OTLP services each declare a method called Export with a different
// signature, so they cannot share one receiver type. They share the capture
// instead.
type traceService struct {
	coltracepb.UnimplementedTraceServiceServer
	c *captured
}

func (s *traceService) Export(_ context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	s.c.addSpans(req.GetResourceSpans())
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

type metricsService struct {
	colmetricspb.UnimplementedMetricsServiceServer
	c *captured
}

func (s *metricsService) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	s.c.addMetrics(req.GetResourceMetrics())
	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

// startCollector listens on a real loopback port. A real socket rather than a
// bufconn on purpose: obs.Setup takes an address, not a dialer, so anything
// that fakes the transport would stop testing the one line -- WithEndpoint --
// that a deployment actually gets wrong.
func startCollector(t *testing.T) (*captured, string) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	c := &captured{}
	srv := grpc.NewServer()
	coltracepb.RegisterTraceServiceServer(srv, &traceService{c: c})
	colmetricspb.RegisterMetricsServiceServer(srv, &metricsService{c: c})

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return c, lis.Addr().String()
}

// exportOneQuote runs the service exactly as cmd/service wires it, serves one
// request, and then shuts the providers down -- which is what forces the batch
// span processor and the periodic reader to flush. Shutdown returning is the
// synchronisation point: the exporter's unary RPC has completed by then, so
// there is nothing to poll for and no sleep to tune.
func exportOneQuote(t *testing.T) ([]*tracepb.ResourceSpans, []*metricspb.ResourceMetrics) {
	t.Helper()

	// Set deliberately, not cleared: newResource reads this, and the precedence
	// between it and the values in Config is asserted below.
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=from-env,tenant.id=acme")

	c, addr := startCollector(t)

	cfg := obs.Config{
		ServiceName:      "quote-api",
		ServiceNamespace: "platform",
		ServiceVersion:   "test",
		Environment:      "test",
		InstanceID:       "test-1",
		OTLPEndpoint:     addr,
	}

	// Every wait has a ceiling: a receiver that never answers must fail the
	// test, not hang the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	shutdown, err := obs.Setup(ctx, cfg)
	if err != nil {
		t.Fatalf("obs.Setup: %v", err)
	}

	inj := faults.NewInjector()
	inj.Set(faults.Config{}) // no injected latency: keep the test fast

	// Wrapped exactly as cmd/service wraps it, including the identity
	// middleware on the dependency. Handlers must be built AFTER obs.Setup:
	// otelhttp reads the global meter provider when the handler is constructed,
	// so one built earlier would record into the provider Setup replaced.
	dep := httptest.NewServer(
		otelhttp.NewHandler(obs.MetricIdentityMiddleware(cfg)(downstream.Handler(inj)), "pricing.server"))
	t.Cleanup(dep.Close)

	srv := &api.Server{
		Cfg:      cfg,
		Log:      slog.New(slog.DiscardHandler),
		Pricing:  downstream.NewClient(dep.URL),
		Injector: inj,
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/quote?sku=SKU-TEST")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown (this is the flush; an error here means telemetry was dropped): %v", err)
	}

	spans, metrics := c.snapshot()
	if len(spans) == 0 {
		t.Fatal("no ResourceSpans reached the collector")
	}
	if len(metrics) == 0 {
		t.Fatal("no ResourceMetrics reached the collector")
	}
	return spans, metrics
}

func attrValue(kvs []*commonpb.KeyValue, key string) (string, bool) {
	for _, kv := range kvs {
		if kv.GetKey() == key {
			return kv.GetValue().GetStringValue(), true
		}
	}
	return "", false
}

// TestTelemetryReachesAnOTLPCollector is the evidence behind the claim the
// README makes: that this service emits OTLP a platform can consume, and that
// the exemplar on a latency histogram points at a trace that exists.
func TestTelemetryReachesAnOTLPCollector(t *testing.T) {
	spans, metrics := exportOneQuote(t)

	t.Run("spans arrive identified as the service", func(t *testing.T) {
		var names []string
		var traceIDs = map[string]bool{}
		var identified int

		for _, rs := range spans {
			name, _ := attrValue(rs.GetResource().GetAttributes(), "service.name")
			ns, _ := attrValue(rs.GetResource().GetAttributes(), "service.namespace")
			if name == "quote-api" && ns == "platform" {
				identified++
			}
			for _, ss := range rs.GetScopeSpans() {
				for _, s := range ss.GetSpans() {
					names = append(names, s.GetName())
					traceIDs[hex.EncodeToString(s.GetTraceId())] = true
				}
			}
		}

		if identified == 0 {
			t.Error("no ResourceSpans carried service.name=quote-api and service.namespace=platform")
		}
		if len(names) == 0 {
			t.Fatal("ResourceSpans arrived carrying no spans")
		}

		// The hand-rolled span is the one automatic instrumentation cannot
		// produce, and the one the README's whole argument rests on. If
		// otelhttp's spans arrive and this does not, the export path is fine
		// and the instrumentation argument is not.
		var sawManual bool
		for _, n := range names {
			if n == "pricing.apply_discount" {
				sawManual = true
			}
		}
		if !sawManual {
			t.Errorf("the hand-rolled span pricing.apply_discount did not arrive; got %v", names)
		}
		t.Logf("spans arrived: %d (%v)", len(names), names)
	})

	t.Run("the traceparent survived the loopback hop as one trace", func(t *testing.T) {
		ids := map[string]int{}
		for _, rs := range spans {
			for _, ss := range rs.GetScopeSpans() {
				for _, s := range ss.GetSpans() {
					ids[hex.EncodeToString(s.GetTraceId())]++
				}
			}
		}
		// Broken propagation raises no error. It produces two valid traces
		// nobody notices were supposed to be one, so the assertion has to be on
		// the count of distinct trace ids rather than on spans existing.
		if len(ids) != 1 {
			t.Errorf("spans span %d distinct trace ids, want 1: %v", len(ids), ids)
		}
		for id, n := range ids {
			t.Logf("trace %s carries %d spans across the hop", id, n)
		}
	})

	t.Run("the histogram the platform queries arrives with identity on the data point", func(t *testing.T) {
		var points, identified int
		for _, rm := range metrics {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					if m.GetName() != "http.server.request.duration" {
						continue
					}
					for _, dp := range m.GetHistogram().GetDataPoints() {
						points++
						name, _ := attrValue(dp.GetAttributes(), "service.name")
						ns, _ := attrValue(dp.GetAttributes(), "service.namespace")
						if name == "quote-api" && ns == "platform" {
							identified++
						}
					}
				}
			}
		}
		if points == 0 {
			t.Fatal("http.server.request.duration did not reach the collector")
		}
		// Every data point, not merely one: the collector's remote-write
		// exporter builds Prometheus labels from data point attributes only, so
		// a server in this process that skipped the middleware becomes a series
		// with both labels empty rather than an error.
		if identified != points {
			t.Errorf("%d of %d duration data points carried the identity attributes", identified, points)
		}
		t.Logf("duration data points on the wire: %d", points)
	})

	t.Run("exemplars arrive pointing at a trace that also arrived", func(t *testing.T) {
		arrived := map[string]bool{}
		for _, rs := range spans {
			for _, ss := range rs.GetScopeSpans() {
				for _, s := range ss.GetSpans() {
					arrived[hex.EncodeToString(s.GetTraceId())] = true
				}
			}
		}

		var total, linked int
		for _, rm := range metrics {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					if m.GetName() != "http.server.request.duration" {
						continue
					}
					for _, dp := range m.GetHistogram().GetDataPoints() {
						for _, ex := range dp.GetExemplars() {
							total++
							if arrived[hex.EncodeToString(ex.GetTraceId())] {
								linked++
							}
						}
					}
				}
			}
		}

		// This is the click in the README, asserted rather than screenshotted.
		// An exemplar that survives the SDK but not the exporter renders a
		// panel with no dots; one carrying a trace id no backend has renders
		// dots that link nowhere. Both look correct until someone clicks.
		if total == 0 {
			t.Fatal("no exemplars reached the collector; the p99 panel would render no dots")
		}
		if linked == 0 {
			t.Error("exemplars arrived but none named a trace that also arrived; every dot would link nowhere")
		}
		t.Logf("exemplars on the wire: %d, of which %d name a trace that arrived", total, linked)
	})

	t.Run("config beats OTEL_RESOURCE_ATTRIBUTES, which still contributes", func(t *testing.T) {
		// resource.New merges detectors in order and the LAST one wins, so the
		// values in Config override the environment for the keys they share.
		// The environment is not ignored: keys it alone sets still ride along.
		// Worth asserting because the opposite is the natural assumption, and
		// getting it backwards means a service that cannot be renamed by the
		// deployment that runs it.
		var checked int
		for _, rs := range spans {
			attrs := rs.GetResource().GetAttributes()
			if got, ok := attrValue(attrs, "service.name"); ok {
				checked++
				if got != "quote-api" {
					t.Errorf("service.name = %q, want quote-api (Config must beat OTEL_RESOURCE_ATTRIBUTES)", got)
				}
			}
			if got, ok := attrValue(attrs, "tenant.id"); !ok {
				t.Error("tenant.id from OTEL_RESOURCE_ATTRIBUTES did not reach the resource")
			} else if got != "acme" {
				t.Errorf("tenant.id = %q, want acme", got)
			}
		}
		if checked == 0 {
			t.Fatal("no resource carried service.name at all")
		}
	})
}
