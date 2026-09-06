# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Nothing has been released. There are no tags and no versioned artifacts, so
everything below is unreleased — that is a statement of fact rather than a
placeholder, and [ROADMAP.md](ROADMAP.md) says what would have to be true
before an image is published.

## [Unreleased]

### Added

- **The service and its OpenTelemetry wiring.** An HTTP API and a simulated
  pricing dependency in one process, talking over real loopback HTTP so the W3C
  `traceparent` header is genuinely serialised, sent and parsed. Traces and
  metrics leave over OTLP/gRPC; logs go to stdout as JSON with `trace_id` and
  `span_id` on every record written inside a span. `internal/obs` is the only
  package that imports the OpenTelemetry SDK.
- **A hand-rolled span around the work `otelhttp` cannot see.**
  `pricing.apply_discount`, which is what makes a slow dependency and a slow
  calculation look different on a waterfall instead of identical.
- **`make up`** — the whole demonstration in one command: the service, an
  OpenTelemetry collector, Prometheus with exemplar storage enabled, Jaeger,
  Grafana opening straight onto a provisioned RED dashboard, and a load
  generator so there is a line to draw on arrival. Clicking an exemplar on the
  p99 panel opens that exact request's trace.
- **Runtime fault injection.** `make slow`, `make errors` and `make reset`
  reshape the latency tail and the error rate without a restart, so the panels
  have something to show on demand.
- **A pre-push gate, committed before anything it guards.** Canonical author and
  committer on every commit in the push range, no attribution terms in either
  the message or the tree, and gitleaks over history — failing closed if the
  scanner is absent, because a secrets gate that skips when its scanner is
  missing is not a gate. `make init` installs it; `make test-hook` runs it
  against six kinds of deliberately bad history.
- **CI**: gofmt, `go vet`, `go build`, `go test -race`, and the same gate file
  in `--all-history` mode with `fetch-depth: 0`, because the hook is per-clone
  configuration that no clone can be trusted to have installed.
- **Proof that the telemetry leaves the process.**
  `internal/obs/otlp_export_test.go` starts a real OTLP/gRPC receiver on a
  loopback port, points the real `obs.Setup` at it, serves one request through
  handlers wired exactly as `cmd/service` wires them, and asserts on the bytes a
  collector would have received: spans carrying the service identity including
  the hand-rolled one, a single trace id across the loopback hop, the duration
  histogram with identity on every data point, and exemplars naming a trace that
  arrived in the same export. Confirmed non-vacuous against four separate
  mutations of the code it guards.
- **`golangci-lint`** with the sibling repositories' linter set and revive rules,
  pinned to `v2.13.1`. Reports zero issues.
- **`govulncheck`** pinned to `v1.7.0`, reporting only advisories on call paths
  this code reaches. Zero found.
- **`gitleaks` as its own CI job** over full history, alongside the copy inside
  the pre-push gate.
- **Renovate**, with `dependencyDashboardApproval` so it writes one dashboard
  issue and never a branch or a pull request.
- **[SECURITY.md](SECURITY.md)**, **[DESIGN.md](DESIGN.md)** and
  **[ROADMAP.md](ROADMAP.md)**.

### Changed

- **The identity middleware moved to `internal/obs` and was exported**, because
  the rule is not "the API needs this" but "every `otelhttp`-wrapped server in
  the process needs this", and a private helper made the narrower reading easy
  to act on.
- **The README leads with the result.** The Grafana panel, the trace behind one
  of its exemplars, and the boundary drawing come before the instructions,
  because the pivot is the thesis. The gap between the p99 line near 2.4s and
  the exemplars near 1.2s is explained rather than cropped: `otelhttp`'s bucket
  boundaries step straight from 1s to 2.5s, so `histogram_quantile` interpolates
  and overshoots, while an exemplar cannot be wrong about its own duration.
- **Every third-party reference is now immutable.** Two actions pinned by commit
  SHA across three uses, and seven container images pinned by digest across the
  Dockerfile and the compose stack. No versions were upgraded in the process —
  pinning and upgrading are separate decisions.
- **`misspell` is set to the UK locale**, matching the English this repository is
  actually written in rather than importing a sibling's answer to the same
  question.

### Fixed

- **The dependency server emitted a nameless series.** The pricing server was
  wrapped in `otelhttp` for tracing but never passed through the identity
  middleware, so it produced `http.server.request.duration` with both identity
  labels empty — which the platform's `sum by (service_name, service_namespace)`
  renders as a nameless row carrying that dependency's latency beside the real
  service's. Nothing errored. Running the stack found it; the suite did not,
  because the test served the dependency bare and so had no server metrics for
  the assertion to be wrong about. The test now wires its dependency exactly as
  `cmd/service` does and asserts on every data point.
- **`GET /admin/inject` reported nanoseconds under field names ending `_ms`.** A
  `time.Duration` is an int64 count of nanoseconds, so a raw Duration tagged
  `json:"base_latency_ms"` serialises 10ms as 10000000. POST had always read
  milliseconds correctly, so the API accepted one unit and reported another, a
  factor of a million apart, with no error anywhere. `Config` now has explicit
  JSON methods over a wire struct in whole milliseconds, and the test
  round-trips rather than checking one direction.
- **The code described its own configuration precedence backwards.** The comment
  on `resource.WithFromEnv` claimed `OTEL_RESOURCE_ATTRIBUTES` "still works and
  wins". `resource.New` merges detectors in order and the last wins, so the
  values in `Config` override the environment for the five keys they share;
  keys the environment alone sets do ride along. Asserted over the exported
  resource now rather than described.
- **`Injector.Get` and `Injector.Set` had no doc comments**, in a type whose
  entire reason for existing is concurrent access. Surfaced by revive.

### Security

- The runtime image is `gcr.io/distroless/static-debian12`, non-root, carrying
  the binary and nothing else — now pinned by digest, as is the build stage.
- Advisory scanning, secret scanning and immutable third-party pins all run in
  CI on every push and pull request.
- [SECURITY.md](SECURITY.md) states what is worth reporting — telemetry that
  lies, rather than the two endpoints — and lists what is deliberately unsafe
  because this is a demonstration: `/admin/inject` has no authentication and
  shares a listener with the API, OTLP has no TLS path, inbound `traceparent` is
  trusted, everything is sampled, and the local stack authenticates nobody.
