package obs

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// MetricIdentityMiddleware copies service.name and service.namespace onto the
// metric data points otelhttp emits for the request.
//
// This lives here, and is exported, because EVERY otelhttp-wrapped server in the
// process needs it -- not just the public API. A server that skips it still
// emits http.server.request.duration, just with no identity attributes, and the
// platform's `sum by (service_name, service_namespace)` then produces an extra
// series with both labels empty. On a dashboard that is a blank row whose
// latency is silently mixed into the service's aggregate; it does not look like
// an error, it looks like a second service with no name.
//
// It must be wrapped INSIDE otelhttp.NewHandler, because the Labeler it writes
// to is placed in the context by otelhttp's own handler. Wrapping it outside
// adds attributes to a Labeler nothing reads, and the labels silently vanish.
func MetricIdentityMiddleware(c Config) func(http.Handler) http.Handler {
	attrs := c.MetricIdentityAttributes()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
				labeler.Add(attrs...)
			}
			next.ServeHTTP(w, r)
		})
	}
}
