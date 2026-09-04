// Package obs contains the OpenTelemetry wiring for this service.
//
// It is deliberately the only package that imports the OTel SDK. Everything
// else in the service uses the API (go.opentelemetry.io/otel/...) and never the
// SDK, so the choice of exporter, sampler and reader lives in exactly one place
// and the business code cannot accidentally depend on it.
package obs

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config is the whole of this service's observability configuration. Every
// field has an environment variable behind it, because the one thing an app
// team must be able to change without a rebuild is where telemetry goes.
type Config struct {
	ServiceName      string // -> service.name
	ServiceNamespace string // -> service.namespace
	ServiceVersion   string // -> service.version
	Environment      string // -> deployment.environment
	InstanceID       string // -> service.instance.id
	OTLPEndpoint     string // host:port, gRPC
}

// ConfigFromEnv reads the standard OTEL_* variables where they exist and falls
// back to values that make the local compose stack work with no setup.
func ConfigFromEnv() Config {
	host, _ := os.Hostname()
	return Config{
		ServiceName:      env("OTEL_SERVICE_NAME", "quote-api"),
		ServiceNamespace: env("SERVICE_NAMESPACE", "platform"),
		ServiceVersion:   env("SERVICE_VERSION", "dev"),
		Environment:      env("DEPLOYMENT_ENVIRONMENT", "local"),
		InstanceID:       env("SERVICE_INSTANCE_ID", host),
		OTLPEndpoint:     env("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ResourceAttributes returns the identity attributes as a plain slice.
//
// These are also attached to every HTTP metric data point, not just to the
// Resource. That is not redundancy, it is a hard requirement of the platform
// side: the collector's prometheusremotewrite exporter builds Prometheus labels
// from DATA POINT attributes only. Resource attributes are used for exactly two
// things there -- service.name and service.namespace are joined into the `job`
// label, and service.instance.id becomes `instance`. Everything else on the
// Resource goes to the separate `target_info` metric and never becomes a label
// on the series itself.
//
// So a recording rule that says `sum by (service_name, service_namespace)`
// matches nothing at all unless one of two things is true: the collector sets
// resource_to_telemetry_conversion.enabled (a platform-side change), or the
// application puts these on the data points (an application-side change). This
// service does the second, because instrumentation is the application team's
// job and this repository should not require an edit to the platform to work.
//
// See README.md, "Why service.name is attached twice".
func (c Config) ResourceAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.ServiceName(c.ServiceName),
		semconv.ServiceNamespace(c.ServiceNamespace),
		semconv.ServiceVersion(c.ServiceVersion),
		semconv.ServiceInstanceID(c.InstanceID),
		semconv.DeploymentEnvironment(c.Environment),
	}
}

// MetricIdentityAttributes are the subset that must ride on every metric data
// point for the platform's recording rules to aggregate correctly.
func (c Config) MetricIdentityAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.ServiceName(c.ServiceName),
		semconv.ServiceNamespace(c.ServiceNamespace),
	}
}

func newResource(ctx context.Context, c Config) (*resource.Resource, error) {
	r, err := resource.New(ctx,
		resource.WithFromEnv(), // OTEL_RESOURCE_ATTRIBUTES still works and wins
		resource.WithTelemetrySDK(),
		resource.WithAttributes(c.ResourceAttributes()...),
	)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}
	return r, nil
}
