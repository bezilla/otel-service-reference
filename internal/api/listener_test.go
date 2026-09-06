package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bezilla/otel-service-reference/internal/downstream"
	"github.com/bezilla/otel-service-reference/internal/faults"
	"github.com/bezilla/otel-service-reference/internal/obs"
)

// The injector has no authentication and exists to break the service. What
// keeps it off the public internet is not a check inside this process -- it is
// that it answers on a port nothing publishes.
//
// That is only true while the two muxes stay separate, and moving one route
// back across the line is a one-word edit that nothing else would notice: the
// service would still start, still serve, still emit correct telemetry, and
// still pass every other test in this package. So the separation is asserted
// directly, in both directions.
func newTestServer(t *testing.T) *Server {
	t.Helper()

	inj := faults.NewInjector()
	dep := httptest.NewServer(downstream.Handler(inj))
	t.Cleanup(dep.Close)

	return &Server{
		Cfg:      obs.Config{ServiceName: "quote-api", ServiceNamespace: "platform"},
		Log:      slog.New(slog.DiscardHandler),
		Pricing:  downstream.NewClient(dep.URL),
		Injector: inj,
	}
}

func status(t *testing.T, h http.Handler, method, target, body string) int {
	t.Helper()

	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("content-type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// TestTheInjectorIsNotOnThePublicListener is the assertion behind the security
// claim. The public mux is the one a Kubernetes Service targets and a gateway
// routes to, so anything reachable here is reachable from outside the cluster.
func TestTheInjectorIsNotOnThePublicListener(t *testing.T) {
	public := newTestServer(t).Routes()

	for _, tc := range []struct{ method, target, body string }{
		{"GET", "/admin/inject", ""},
		{"POST", "/admin/inject", `{"error_rate":1.0}`},
	} {
		if got := status(t, public, tc.method, tc.target, tc.body); got != http.StatusNotFound {
			t.Errorf("%s %s on the public listener = %d, want 404 -- the injector is reachable through the gateway",
				tc.method, tc.target, got)
		}
	}
}

// The public listener must keep serving everything it is actually for. Moving
// the injector off it is worth nothing if it took the API with it.
func TestThePublicListenerStillServesTheService(t *testing.T) {
	public := newTestServer(t).Routes()

	if got := status(t, public, "GET", "/api/quote?sku=SKU-TEST", ""); got != http.StatusOK {
		t.Errorf("GET /api/quote = %d, want 200", got)
	}
	if got := status(t, public, "GET", "/healthz", ""); got != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200", got)
	}
}

// And the admin listener serves the injector and nothing else. A reader who
// finds this port should not also find the API on it, because a second copy of
// the public surface on an unpublished port is a second thing to reason about.
func TestTheAdminListenerServesOnlyTheInjector(t *testing.T) {
	admin := newTestServer(t).AdminRoutes()

	if got := status(t, admin, "GET", "/admin/inject", ""); got != http.StatusOK {
		t.Errorf("GET /admin/inject on the admin listener = %d, want 200", got)
	}
	if got := status(t, admin, "POST", "/admin/inject", `{"error_rate":0.2}`); got != http.StatusOK {
		t.Errorf("POST /admin/inject on the admin listener = %d, want 200", got)
	}
	for _, target := range []string{"/api/quote", "/healthz"} {
		if got := status(t, admin, "GET", target, ""); got != http.StatusNotFound {
			t.Errorf("GET %s on the admin listener = %d, want 404", target, got)
		}
	}
}
