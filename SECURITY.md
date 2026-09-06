# Security

## Scope

This is a reference service built to be read and to be run on a laptop. It
holds no credentials, talks to no real dependency, and serves a price it made
up. The interesting attack surface is not the two endpoints — it is the
instrumentation, because that is what the repository asks you to trust.

**The most valuable report here is telemetry that lies.** Anything that makes
this service emit something an operator would reasonably act on and be wrong
about is the highest-severity defect in the repository, regardless of how
contrived the path is:

- a span, metric or log line that carries an attribute belonging to a different
  request;
- trace context that crosses a boundary it should not — a `traceparent`
  accepted from an untrusted caller and joined to an internal trace without a
  word about it;
- an identity attribute that can be set by anything other than this service's
  own configuration, since the platform aggregates on those two labels;
- anything that puts request content into a span, a metric label or a log field
  where it was not intended, which is the shape most telemetry data leaks take;
- unbounded label cardinality reachable from a request, which is a denial of
  service against the metrics backend rather than against this process.

Out of scope: the numbers being unrealistic. The latency and error rates are
injected on purpose and are tunable at runtime by design.

## Reporting

Open a [private security advisory](https://github.com/bezilla/otel-service-reference/security/advisories/new)
rather than a public issue, or email **bezilla@protonmail.com**. Include the
affected package and what an operator would have concluded from the bad
telemetry. No response time is promised — this is a personal project
maintained by one person. A proof of concept is welcome but not required.

## Supported versions

`main`, and only `main`. There are no releases, no tags and no maintenance
branches; a fix lands on `main` and that is the supported version. If you are
running this in a way where that matters, you are running it further than it
was meant to go.

## What this repository does to stay clean

- **No secrets in Git.** gitleaks runs over full history inside the pre-push
  gate, which fails closed if the scanner is missing rather than skipping. CI
  runs that same gate file in the `identity` job, so the scan happens whether or
  not anyone installed the hook.
- **Every third-party reference is immutable.** Actions by commit SHA,
  container images by digest in both the Dockerfile and the compose stack, the
  linter and the vulnerability scanner by version. Renovate keeps them current
  without being able to open a pull request.
- **Advisories are scanned on reachable paths.** `govulncheck` runs in CI
  against the toolchain that builds the binary.
- **The runtime image carries the binary and nothing else.**
  `gcr.io/distroless/static-debian12`, non-root, no shell, no package manager.
- **No metrics endpoint.** Telemetry leaves over OTLP only; there is no
  `/metrics` to scrape and nothing listening that was not asked for.

## What is deliberately insecure, because it is a demonstration

Every one of these is fine on a laptop and would be a serious defect anywhere
real. They are listed rather than fixed because the repository is meant to be
read, and a reader should be able to see the whole of what it chose not to do.

- **`/admin/inject` has no authentication**, and will not be given any. It
  exists to break the service on purpose; a credential would only mean the
  injector is one leaked string away rather than zero. **Fixed in
  `546bb32`:** it is no longer served on the public listener. It answers on
  `ADMIN_ADDR` (`:8082`) only, returns 404 on `:8080`, and is asserted in both
  directions in `internal/api/listener_test.go`.

  It is still unauthenticated, so the exposure decision moves to whoever
  publishes the port. Do not put `:8082` in a Service, an ingress or a load
  balancer. The sibling platform's chart publishes exactly one port and it is
  not this one; the local compose stack publishes it deliberately, because
  `make slow` has to reach it from a laptop.
- **OTLP is exported without TLS.** `WithInsecure()` is unconditional; there is
  no code path that dials a collector securely. Every real deployment needs one
  added.
- **Every trace is sampled.** `AlwaysSample()` is correct for watching a
  dashboard fill in and wrong for anything with traffic.
- **Inbound trace context is trusted.** The W3C propagator joins whatever
  `traceparent` a caller sends. That is the correct default behind a gateway
  that strips it and the wrong one on an internet-facing listener.
- **The local stack has no authentication anywhere.** Grafana runs with
  anonymous access at Admin, with the login form disabled; Prometheus and
  Jaeger are unauthenticated. `make up` is for `localhost` and binds there.
