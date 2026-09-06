# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Nothing has been released. There are no tags and no versioned artifacts, so
everything below is unreleased — that is a statement of fact rather than a
placeholder, and [ROADMAP.md](ROADMAP.md) says what would have to be true
before an image is published.

## [Unreleased]

### Changed

- **The identity gate allowlists trailers instead of searching for vendor
  names.** Only `Signed-off-by` carrying exactly `Paul Bezilla
  <bezilla@protonmail.com>`, `Verified` and `Measured` may appear in a trailer
  block; every other key is refused. The scan it replaced matched nothing across
  207 commits of full history in all six repositories in this family. Trailers
  are read with `git interpret-trailers --parse` — git's own definition — because
  a `^Key:` regex would reject ordinary prose, including five lines in this
  repository.
- **Annotated tags are checked**, which nothing did before: the tagger must be the
  canonical identity and the annotation body is subject to the same allowlist.
  `v0.1.0` passes as it stands.
- **The self-test proves both directions**, thirteen cases, and captures the
  hook's status with `|| rc=$?` rather than reading `$?` from a bare command,
  which is silently fatal under the `bash -eo pipefail` CI runs steps with. It is
  exercised under `-e`, plain bash and its shebang with identical results.
- **The standalone `gitleaks` job is gone**; the scan lives only in the gate
  file now. `gitleaks-action` requires a `GITHUB_TOKEN` to scan a pull request
  and was never given one, so on every PR it failed before scanning anything —
  and because it was not a required check, it failed where nobody had to look.
  The `identity` job already runs `gitleaks git` over full history with
  `fetch-depth: 0`, against the same default ruleset, so no coverage moved with
  it. What is lost is independence: the gate's stages are sequential, and a
  failure on identity or trailers now exits before the secrets stage runs.

**History was not rewritten.** No force push, no retag. Both gates were run over
all 34 commits and the one tag first: old accepted 34 / rejected 0, new accepted
34 / rejected 0, disagreements 0.


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
- **Renovate**, with `dependencyDashboardApproval` so it writes one dashboard
  issue and never a branch or a pull request.
- **[SECURITY.md](SECURITY.md)**, **[DESIGN.md](DESIGN.md)** and
  **[ROADMAP.md](ROADMAP.md)**.

### Changed

- **The fault injector moved to its own listener.** `/admin/inject` is served on
  `ADMIN_ADDR` (`:8082`) and returns 404 on the API port. It is still
  unauthenticated by design — it exists to break the service on purpose — so the
  separation is a port nothing publishes rather than a credential. `make slow`,
  `make errors` and `make reset` talk to the new port, which the compose stack
  publishes to the host and the sibling platform's chart does not publish at
  all. The admin mux is uninstrumented, so injecting latency no longer records
  itself on the latency panel it exists to move.
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

- **The injector was reachable through a public gateway.** On its own, an
  unauthenticated `/admin/inject` on the API listener was a documented sharp
  edge. Deployed through the sibling platform's paved-road chart it was a live
  one: that chart publishes one port through an HTTPRoute attached to a public
  gateway, so anything that could reach `/api/quote` from outside the cluster
  could set the error rate to 1.0 for every request after it. Neither repository
  was wrong about itself; the exposure lived in the seam between them. Asserted
  in both directions now rather than documented.
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
- The fault injector is off the public listener, so the one endpoint that can
  degrade every subsequent request is no longer reachable through the gateway
  the sibling platform attaches to this workload.
- [SECURITY.md](SECURITY.md) states what is worth reporting — telemetry that
  lies, rather than the two endpoints — and lists what remains deliberately
  unsafe because this is a demonstration: OTLP has no TLS path, inbound
  `traceparent` is trusted, everything is sampled, and the local stack
  authenticates nobody.
