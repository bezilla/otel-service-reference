// Command service runs the reference application: an HTTP API, a simulated
// downstream dependency it calls over real HTTP, and the OpenTelemetry wiring
// that makes both observable.
//
// Both roles run in one process so the whole demonstration is one command. They
// still talk over the loopback network rather than through a function call, so
// the W3C traceparent header is genuinely serialised, sent and parsed -- which
// is the part that breaks in real systems.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/bezilla/otel-service-reference/internal/api"
	"github.com/bezilla/otel-service-reference/internal/downstream"
	"github.com/bezilla/otel-service-reference/internal/faults"
	"github.com/bezilla/otel-service-reference/internal/obs"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := obs.ConfigFromEnv()
	log := obs.NewLogger(cfg)

	shutdown, err := obs.Setup(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		// A fresh context: the one above is already cancelled by the time we
		// get here, and a cancelled context flushes nothing.
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(flushCtx); err != nil {
			log.Error("telemetry shutdown", slog.Any("error", err))
		}
	}()

	inj := faults.NewInjector()

	apiAddr := envOr("API_ADDR", ":8080")
	depAddr := envOr("PRICING_ADDR", ":8081")
	depURL := envOr("PRICING_URL", "http://localhost:8081")

	srv := &api.Server{
		Cfg:      cfg,
		Log:      log,
		Pricing:  downstream.NewClient(depURL),
		Injector: inj,
	}

	apiSrv := &http.Server{
		Addr:              apiAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	// The dependency is instrumented too. If it were not, the trace would show
	// the client span and then stop, and the propagation would be untestable.
	//
	// It gets the identity middleware as well. Every otelhttp-wrapped server in
	// the process needs it: one that skips it emits the same histogram with no
	// service_name or service_namespace, which the platform aggregates into an
	// extra series with both labels empty -- a nameless row on every RED panel,
	// carrying this dependency's latency alongside the real service's.
	depSrv := &http.Server{
		Addr: depAddr,
		Handler: otelhttp.NewHandler(
			obs.MetricIdentityMiddleware(cfg)(downstream.Handler(inj)), "pricing.server"),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- serve(apiSrv, "api", apiAddr, log) }()
	go func() { errCh <- serve(depSrv, "pricing", depAddr, log) }()

	log.Info("service up",
		slog.String("api", apiAddr),
		slog.String("pricing", depAddr),
		slog.String("otlp", cfg.OTLPEndpoint),
		slog.String("service_name", cfg.ServiceName),
		slog.String("service_namespace", cfg.ServiceNamespace))

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(apiSrv.Shutdown(shutCtx), depSrv.Shutdown(shutCtx))
}

func serve(s *http.Server, name, addr string, log *slog.Logger) error {
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", slog.String("server", name), slog.String("addr", addr), slog.Any("error", err))
		return err
	}
	return nil
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
