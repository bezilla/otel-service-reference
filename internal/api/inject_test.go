package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bezilla/otel-service-reference/internal/downstream"
	"github.com/bezilla/otel-service-reference/internal/faults"
	"github.com/bezilla/otel-service-reference/internal/obs"
)

// The admin API accepted milliseconds and reported nanoseconds under field
// names ending _ms, because faults.Config tagged raw time.Duration fields as
// _ms. Nothing errored: POST worked, GET returned valid JSON, and the numbers
// were a million times too large.
//
// A test that only checked POST would have passed. A test that only checked GET
// would have needed someone to already know the right answer. Round-tripping is
// what catches it: whatever POST accepts, GET must report back unchanged.
func TestInjectRoundTripsInMilliseconds(t *testing.T) {
	inj := faults.NewInjector()
	dep := httptest.NewServer(downstream.Handler(inj))
	t.Cleanup(dep.Close)

	srv := &Server{
		Cfg:      obs.Config{ServiceName: "quote-api", ServiceNamespace: "platform"},
		Log:      slog.New(slog.DiscardHandler),
		Pricing:  downstream.NewClient(dep.URL),
		Injector: inj,
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	sent := map[string]any{
		"base_latency_ms": 25,
		"tail_latency_ms": 900,
		"tail_percent":    0.3,
		"error_rate":      0.1,
	}
	body, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := http.Post(ts.URL+"/admin/inject", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}

	got, err := http.Get(ts.URL + "/admin/inject")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = got.Body.Close() }()

	var back map[string]float64
	if err := json.NewDecoder(got.Body).Decode(&back); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for k, v := range sent {
		want, _ := v.(int)
		wantF := float64(want)
		if f, ok := v.(float64); ok {
			wantF = f
		}
		if back[k] != wantF {
			t.Errorf("%s: POST accepted %v, GET reported %v", k, wantF, back[k])
		}
	}
}

// The internal representation must stay a real Duration: the wire unit is a
// presentation choice and should not leak into the latency arithmetic.
func TestInjectStoresRealDurations(t *testing.T) {
	inj := faults.NewInjector()
	var c faults.Config
	if err := json.Unmarshal([]byte(`{"base_latency_ms":25,"tail_latency_ms":900}`), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	inj.Set(c)

	if got := inj.Get().BaseLatency.Milliseconds(); got != 25 {
		t.Errorf("BaseLatency = %dms, want 25ms", got)
	}
	if got := inj.Get().TailLatency.Milliseconds(); got != 900 {
		t.Errorf("TailLatency = %dms, want 900ms", got)
	}
}
