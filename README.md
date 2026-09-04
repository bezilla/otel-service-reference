# otel-service-reference

A small, fully instrumented Go HTTP service. It is the **application half** of an
observability boundary whose platform half lives in a different repository.

The platform side — collector pipelines, recording rules, dashboards, SLO
alerting — is infrastructure the platform team owns. The instrumentation is not.
It belongs to the team that owns the service, because it is written in their
code, in their language, about their domain, and no amount of collector
configuration can invent a span around a decision only they know they made.

That argument is easy to assert and easy to quietly violate. So this is a
separate repository on purpose: **two repositories that share nothing but OTLP
demonstrate the boundary by existing.** Nothing here imports anything there.
Nothing there reaches into this code. They agree on a wire protocol and a
handful of attribute names, and that is the entire contract.

## What you get in one command

```sh
make up
```

Grafana on <http://localhost:3000>, no login, opening straight onto the
dashboard. Traffic is already flowing. Give it about thirty seconds, then:

**Click a dot on the p99 latency panel.** You land on that exact request's trace
in Jaeger — the HTTP server span, the client span, the downstream server span
and the hand-rolled business span, with the slow one visible.

That click is the whole point of the repository. It is also the thing that
breaks in four separate places, silently, and most of this README is about
those four places.

```sh
make slow      # widen the latency tail, so p99 has something to show
make errors    # 20% 5xx, so the error panel moves
make reset     # back to healthy
make logs      # JSON logs, trace_id on every line
make down
```

---

## The instrumentation, decision by decision

### 1. Resource identity, and why `service.name` is attached twice

The service sets the usual resource attributes — `service.name`,
`service.namespace`, `service.version`, `service.instance.id`,
`deployment.environment`. It then attaches `service.name` and
`service.namespace` **a second time, onto every HTTP metric data point**
(`internal/obs/identity.go`, via otelhttp's `Labeler`).

Every otelhttp-wrapped server in the process needs that middleware, not just the
public API. Running the stack is what proved it: the dependency server had been
wrapped for tracing but not for identity, so it emitted the same histogram with
both labels empty, and the platform's aggregation rendered it as a nameless row
carrying the dependency's latency beside the real service's. Nothing errored.

That looks like a mistake. It is not, and this is the least obvious thing here.

The collector's `prometheusremotewrite` exporter builds Prometheus labels from
**data point attributes only**. Resource attributes are used for exactly two
things: `service.name` and `service.namespace` are joined into the `job` label,
and `service.instance.id` becomes `instance`. Everything else on the Resource is
shipped off to a separate `target_info` metric and never becomes a label on the
series itself.

So a platform recording rule that says

```promql
sum by (service_name, service_namespace) (rate(http_server_request_duration_seconds_count[5m]))
```

aggregates over **nothing at all** unless one of two things is true:

| Fix | Where it lives | Cost |
|---|---|---|
| `resource_to_telemetry_conversion.enabled: true` on the exporter | Platform repo | Every service gets every resource attribute as a label, on every series |
| Put the two attributes on the data point | This repo | Two extra labels, chosen deliberately |

This repository does the second. It keeps the platform unmodified — which is the
boundary working as intended — and it is the more conservative of the two, since
the alternative silently multiplies label cardinality for every service at once.

The local stack in `deploy/` deliberately does **not** set
`resource_to_telemetry_conversion`. If it did, the demo would pass while the
service stayed broken against a real platform.

### 2. Automatic and manual instrumentation are not alternatives

`otelhttp.NewHandler` gives you the server span and the `http.server.*` metrics
for free. Use it. It knows about HTTP, so it produces exactly what any HTTP
service should produce, identically everywhere, with no chance of a typo in a
span name.

What it cannot do is tell you *which part* of a slow request was slow, because
it does not know what your service does. So `applyDiscount` gets a hand-rolled
span (`internal/api/api.go`):

```
GET /api/quote  ────────────────────────────── 430ms   (otelhttp, automatic)
  ├── GET /pricing ──────────────────────── 425ms      (otelhttp transport)
  │     └── pricing.lookup ─────────────── 420ms       (manual, downstream)
  └── pricing.apply_discount ── 2ms                    (manual, this service)
```

Without the manual spans this is one 430ms bar and every regression looks the
same. The rule of thumb: **automatic instrumentation covers the boundaries a
framework knows about; you add spans around the decisions your service makes.**

### 3. Propagation is only real if it goes over the wire

The "downstream dependency" is a second HTTP server, in the same process, called
over loopback. It could have been a function call — that would still produce a
child span. It is a real HTTP hop specifically so the W3C `traceparent` header
is genuinely serialised, sent, and parsed on the other side.

Broken propagation does not raise an error. It produces two perfectly valid
single-span traces that no one notices are supposed to be one trace. The only
way to catch it is to make the hop real, which is why
`otelhttp.NewTransport` wraps the client (`internal/downstream/downstream.go`) —
that wrapper both creates the client span *and* injects the header.

### 4. Exemplars: what actually turns them on

**Nothing.** There is no flag. This is the part worth reading carefully, because
the intuition that there must be a switch sends people looking for one that does
not exist.

In the Go SDK, the default exemplar filter is already `TraceBasedFilter`. It
records an exemplar whenever a measurement is taken with a context carrying a
**sampled** span. `internal/obs/providers.go` sets no exemplar option at all, on
purpose — naming the default would imply it needed enabling.

What exists instead are four ways to get **no exemplars, with no error message
anywhere**:

1. **Recording the measurement with the wrong context.** `TraceBasedFilter` reads
   the span out of the `context.Context` you pass to `Record`. Pass
   `context.Background()` and you get zero exemplars forever. otelhttp gets this
   right — it calls `Record(ctx, …)` with the live request context — but your own
   instruments are your own problem.
2. **Sampling the span away.** No sampled span, no exemplar. A head sampler at 1%
   drops 99% of your exemplars with it. This service uses `AlwaysSample()`, which
   is right for a demo and wrong for production; expect the dots to thin out when
   you lower it.
3. **Asynchronous instruments.** Observable counters and gauges take no context,
   so `TraceBasedFilter` can never match. The SDK swaps in a drop-everything
   reservoir for that combination. Exemplars only make sense on synchronous
   instruments.
4. **Plotting a recording rule.** Exemplars are attached to raw histogram bucket
   samples and **do not survive rule evaluation**. A panel plotting a recorded
   p99 renders no exemplars no matter what is configured on it. The dashboard
   here queries `http_server_request_duration_seconds_bucket` directly for
   exactly this reason.

Then, downstream of the SDK, three more things must be true:

| Hop | What is required | Failure mode |
|---|---|---|
| Collector | Nothing. The remote-write translator converts OTLP exemplars whenever present and attaches the trace id as `trace_id`. **There is no `export_exemplars` option** — do not go looking for one | — |
| Prometheus | `--enable-feature=exemplar-storage` | Exemplars accepted and silently discarded |
| Grafana | `exemplarTraceIdDestinations` with `name: trace_id` and a `datasourceUid` that resolves | Dots render, click goes nowhere |

`name: trace_id` is not a preference. It is the label the collector's translator
writes. `traceID` or `traceId` produces a dashboard that looks right and links
nowhere.

### 5. Logs carry `trace_id`, and that is the whole pivot

`internal/obs/logging.go` wraps a `slog.JSONHandler` so every record written
inside a span gets `trace_id` and `span_id`. Field naming matters for the same
reason as above: `trace_id`, lower snake case, matching the exemplar label.

Logs go to stdout rather than over OTLP. That is deliberate for a reference
service — stdout is what a container runtime already collects, and it keeps the
service debuggable when no exporter is running.

---

## Pairing with the platform side

The platform repository's recording rules query these names. They are not
invented here; they are the OpenTelemetry semantic convention names, plus the
unit suffix the collector appends.

| What the platform queries | What this service emits |
|---|---|
| `http_server_request_duration_seconds_bucket` | `http.server.request.duration` (unit `s`) |
| `http_server_request_duration_seconds_count` | the same histogram's count |
| `http_response_status_code` | `http.response.status_code` |
| `service_name`, `service_namespace` | attached to the data point, per §1 |

`internal/api/api_test.go` asserts all four against collected metric data. They
are tested rather than documented because each one fails silently: no error, no
warning, just an empty panel found during an incident.

To point this service at a real collector instead of the local stack:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=otel-gateway.observability.svc.cluster.local:4317 \
OTEL_SERVICE_NAME=quote-api \
SERVICE_NAMESPACE=platform \
  ./service
```

That is the entire integration. No shared library, no platform-supplied SDK
wrapper, no code generation.

---

## Layout

```
cmd/service/           entrypoint; runs the API and the dependency
internal/obs/          the ONLY package importing the OTel SDK
internal/api/          handlers, the Labeler middleware, the manual span
internal/downstream/   the simulated dependency and its instrumented client
internal/faults/       latency and error injection
deploy/                collector, Prometheus, Jaeger, Grafana, dashboard
.githooks/             identity and attribution gate, and its selftest
```

Everything outside `internal/obs` imports the OpenTelemetry **API** and never the
SDK. That is what keeps exporter and sampler choices in one file, and what lets a
test swap in a recorder without touching business code.

## What this deliberately does not do

- **No real business logic.** Two endpoints and a fake dependency. The
  instrumentation is the product; anything else would be scenery.
- **No metrics endpoint.** Telemetry leaves over OTLP. Prometheus receives it by
  remote write from the collector, as it does on the platform side.
- **No logs over OTLP.** See §5.
- **No production sampling.** `AlwaysSample()` is a demo choice, called out in
  the code where someone changing it will read it.
- **No auth on `/admin/inject`.** It exists to break the service on purpose,
  which is reason enough not to expose this anywhere.

## Development

```sh
make init      # step 1 in any clone: installs the pre-push gate
make check     # gofmt, vet, race tests, hook selftest
make identity  # run the attribution gate over all history
```

See [CONTRIBUTING.md](CONTRIBUTING.md) before your first commit.

## License

MIT. See [LICENSE](LICENSE).
