package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/bezilla/otel-service-reference/internal/downstream"
	"github.com/bezilla/otel-service-reference/internal/faults"
	"github.com/bezilla/otel-service-reference/internal/obs"
)

// The three assertions in this file are the contract with the platform side.
// Each one is something that fails SILENTLY in production if it regresses: no
// error, no warning, just a dashboard panel that is empty or an exemplar that
// is not there. They assert on collected data rather than on the absence of an
// error for that reason.
func collectQuoteMetrics(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	// AlwaysSample matters: the default exemplar filter is TraceBasedFilter,
	// which records nothing for an unsampled span.
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetMeterProvider(mp)
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
		_ = tp.Shutdown(context.Background())
	})

	inj := faults.NewInjector()
	inj.Set(faults.Config{}) // no injected latency: keep the test fast

	dep := httptest.NewServer(downstream.Handler(inj))
	t.Cleanup(dep.Close)

	srv := &Server{
		Cfg: obs.Config{
			ServiceName:      "quote-api",
			ServiceNamespace: "platform",
			ServiceVersion:   "test",
			Environment:      "test",
			InstanceID:       "test-1",
		},
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

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

func findHistogram(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Histogram[float64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s is %T, want Histogram[float64]", name, m.Data)
			}
			return h
		}
	}
	var names []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names = append(names, m.Name)
		}
	}
	t.Fatalf("metric %q not found; got %v", name, names)
	return metricdata.Histogram[float64]{}
}

// The platform's recording rules query http_server_request_duration_seconds_*.
// That name is not chosen by this repository -- it is the semantic convention
// name plus the unit suffix the collector appends -- so a change to it upstream
// would silently unhook every dashboard.
func TestDurationHistogramUsesTheSemconvName(t *testing.T) {
	rm := collectQuoteMetrics(t)
	h := findHistogram(t, rm, "http.server.request.duration")
	if len(h.DataPoints) == 0 {
		t.Fatal("no data points on the duration histogram")
	}
}

// service.name and service.namespace must be DATA POINT attributes, not just
// Resource attributes. The collector's remote-write translator builds Prometheus
// labels from data point attributes only; resource attributes become the `job`
// label and target_info. Without this, `sum by (service_name, service_namespace)`
// aggregates over an empty set and every RED panel is blank.
func TestServiceIdentityIsOnTheDataPoint(t *testing.T) {
	rm := collectQuoteMetrics(t)
	h := findHistogram(t, rm, "http.server.request.duration")

	for _, dp := range h.DataPoints {
		var gotName, gotNS bool
		for _, kv := range dp.Attributes.ToSlice() {
			switch string(kv.Key) {
			case "service.name":
				gotName = kv.Value.AsString() == "quote-api"
			case "service.namespace":
				gotNS = kv.Value.AsString() == "platform"
			}
		}
		if !gotName || !gotNS {
			t.Errorf("data point attrs %v: want service.name and service.namespace present",
				dp.Attributes.ToSlice())
		}
	}
}

// Exemplars are what make the p99 panel clickable. The SDK records them by
// default via TraceBasedFilter -- there is no flag -- but only when the
// measurement is taken with a context carrying a sampled span. This asserts a
// real trace id came through, because an exemplar with a zero trace id links
// nowhere and looks identical on a dashboard until you click it.
func TestDurationHistogramCarriesExemplarsWithTraceIDs(t *testing.T) {
	rm := collectQuoteMetrics(t)
	h := findHistogram(t, rm, "http.server.request.duration")

	var total int
	for _, dp := range h.DataPoints {
		for _, ex := range dp.Exemplars {
			total++
			var nonZero bool
			for _, b := range ex.TraceID {
				if b != 0 {
					nonZero = true
					break
				}
			}
			if !nonZero {
				t.Errorf("exemplar has an all-zero trace id: %x", ex.TraceID)
			}
		}
	}
	if total == 0 {
		t.Fatal("no exemplars recorded; the metric was recorded without a sampled span in context")
	}
	t.Logf("exemplars recorded: %d", total)
}
