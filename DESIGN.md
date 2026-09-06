# Design

The README argues the instrumentation. This file records the decisions behind
it: what was chosen, what was rejected and why, and the defects that changed
how the repository tests itself.

---

## The shape of the thing

Two HTTP servers in one process, talking to each other over loopback. One is
the API; the other stands in for a dependency. Both are wrapped in `otelhttp`,
both emit OTLP to a single endpoint, and everything they emit is either the
semantic convention or is named to match what the platform side queries.

`internal/obs` is the only package that imports the OpenTelemetry **SDK**.
Everything else imports the **API**. That boundary is not decoration: it is
what keeps exporter, sampler and reader choices in one file, and what lets a
test swap the whole telemetry pipeline without touching a handler.

---

## Decisions, and what was rejected

### Identity on the data point, not on the collector

**Chosen:** attach `service.name` and `service.namespace` to every HTTP metric
data point, via `otelhttp`'s `Labeler`.

**Rejected:** setting `resource_to_telemetry_conversion.enabled: true` on the
collector's `prometheusremotewrite` exporter.

Both make `sum by (service_name, service_namespace)` work. The collector option
is one line and it is somebody else's line — it lives in the platform
repository, it applies to every service at once, and it promotes *every*
resource attribute to a label, multiplying cardinality across the estate to fix
one service's aggregation. Two attributes chosen deliberately is the more
conservative half of the trade, and it keeps the platform unmodified, which is
the boundary this repository exists to demonstrate working.

The local stack deliberately does not set the collector option. If it did, the
demonstration would pass while the service stayed broken against a real
platform — which is the worst available outcome for a reference repository.

### A real HTTP hop, not a function call

**Chosen:** the dependency is a second `http.Server` reached over loopback.

**Rejected:** a function call, which would produce a perfectly good child span
for a fraction of the code.

A function call cannot break propagation. Serialising a trace context into a
`traceparent` header, sending it, and parsing it on the other side can, and does
— and when it breaks it produces two entirely valid single-span traces that
nobody notices were meant to be one. Making the hop real is what puts that
failure inside the reach of a test.

### Logs to stdout, not over OTLP

**Chosen:** JSON on stdout, with `trace_id` and `span_id` stamped on every
record written inside a span.

**Rejected:** the OTLP logs exporter.

The correlation that matters is the field on the line, and that does not require
the line to travel over OTLP. Stdout is what a container runtime already
collects, and it keeps the service debuggable when no exporter is reachable —
which is exactly the moment someone is reading logs. Field naming is not a
preference here: `trace_id`, lower snake case, because that is the label the
collector's remote-write translator attaches to exemplars, and `traceID` would
work for humans while breaking every automatic pivot.

### Saying nothing about exemplars

**Chosen:** set no exemplar option on the meter provider at all.

**Rejected:** naming `TraceBasedFilter` explicitly for clarity.

It is already the default. Naming it would imply it needed enabling, and the
belief that there is a switch is precisely what sends people hunting for one
that does not exist. The four ways to silently get no exemplars are documented
instead, in the README and in the code, because that is where the time actually
goes.

### The dashboard queries raw buckets

Exemplars are attached to raw histogram samples and do not survive rule
evaluation. A panel plotting a recorded p99 renders no exemplars no matter what
is configured on it, and nothing anywhere reports why. So the dashboard queries
`http_server_request_duration_seconds_bucket` directly, and the p99 line it
draws overshoots the exemplars because `otelhttp`'s bucket boundaries step from
1s to 2.5s — which is left visible and explained rather than tuned away, since
it is one of the better arguments for exemplars on the page.

### An in-process OTLP receiver, not the collector image

**Chosen:** the export test starts a real OTLP/gRPC server on a loopback port
inside the test binary.

**Rejected:** running `otel/opentelemetry-collector-contrib` from the compose
stack in CI.

The claim under test is what this service puts on the wire, not what a collector
does with it afterwards. An in-process receiver asserts on the exact bytes, runs
in under two seconds, needs no daemon, and cannot fail for reasons belonging to
somebody else's container. A real socket rather than an in-memory transport,
though: `obs.Setup` takes an address and not a dialer, so faking the transport
would stop exercising the one line a deployment actually gets wrong.

### The injector is separated by a listener, not by a password

**Chosen:** serve `/admin/inject` on a third listener, `ADMIN_ADDR`, defaulting
to `:8082`.

**Rejected:** an auth middleware, a path prefix a gateway could strip, and a
`WithFilter` on the public mux.

All three of the rejected options are decisions taken inside a process that has
already accepted the connection, so each one is exactly as strong as the code
implementing it. Anything that routes traffic to a service — a Kubernetes
Service, a gateway, a load balancer — selects a **port**, so a port that is
published nowhere cannot be reached whatever path is requested and whatever a
middleware later concludes.

Authentication was rejected on top of that for a reason specific to what this
endpoint is. It exists to break the service on purpose. A credential in front of
it does not make breaking the service safe; it means the injector is one leaked
string away rather than zero, and it invites the endpoint to be published on the
grounds that it is now protected.

The shape is not new here either: the pricing dependency has been on its own
unpublished listener since the beginning, for a different reason. Two listeners
nothing routes to is one pattern.

The admin mux is uninstrumented, which is a smaller decision inside the same
one. Operator traffic is not service traffic, and recording `make slow` on the
same histogram as `/api/quote` would draw the act of injecting latency as
service latency, on the panel the injection exists to move.

### The identity gate allowlists trailers instead of hunting for names

**Chosen:** an allowlist on the trailer block — `Signed-off-by` carrying exactly
the canonical identity, `Verified` and `Measured` carrying free text, every other
key refused.

**Rejected:** the scan this replaced, which searched every commit message and
every tree in the push range for a list of vendor and tool names.

Measured before removing it: across the full history of all six repositories in
this family, 207 commits, that search matched **nothing**. Zero in messages, zero
in blobs, zero files flagged. It had never caught anything, and by construction it
only ever could have caught what somebody had already thought to write down.

Any tool that stamps provenance onto a commit does it through a trailer, so the
trailer block is the surface worth policing. An unlisted key is refused for being
unlisted rather than surviving because nobody added it to a list — which is the
difference between a rule that holds for a tool shipping next week and one that
does not.

**Trailers are read with `git interpret-trailers --parse`, not a regex.** That is
git's own definition: the last paragraph, and only when the whole paragraph parses
as trailers. It has an edge worth knowing — whether a `Key: Value` line is a
trailer depends on which paragraph it lands in, so `Verified: ...` followed by
more prose is ordinary text and the same line at the end is a trailer. A `^Key:`
regex would have rejected commits in all six repositories on the day it shipped;
five lines in this one alone (`pins:`, `ships:`, `artefact:`, `diagram:`,
`wanted:`) are prose of exactly that shape.

Annotated tags are checked now, which nothing did before: the tagger must be the
canonical identity and the annotation body goes through the same allowlist,
because otherwise a tag is a place to put a trailer the commit gate refused.

Identity, scope and gitleaks are unchanged — author and committer both canonical,
`refs/heads` and `refs/tags` and deliberately not `refs/remotes` or `refs/pull`,
gitleaks still failing closed. **History was not rewritten.** Both gates were run
over all 34 commits and the one tag before the change landed: old accepted 34,
new accepted 34, and the count the old gate accepts and the new one refuses is 0.

### British English, named rather than assumed

The linter configuration copied from the sibling repositories sets `misspell` to
the US locale. Run that way here it reported seven findings, every one of them a
correctly spelled word in this repository's own voice. The locale is set to UK
instead — the same job that setting does there, with this repository's answer.

### Renovate with pull requests disabled

Every commit here carries one identity in both fields, and every server-side
merge mode rewrites at least one of them, so nothing can land through the merge
button. The sharper constraint is that opening a pull request creates
`refs/pull/N/head`, which GitHub keeps permanently — deleting the branch does
not remove it, and only recreating the repository does. A repository about to be
made public should not acquire permanent refs it did not write. Renovate with
`dependencyDashboardApproval` writes one issue and never a branch.

### No image scanning, for now

`trivy` and `grype` are in none of the sibling Go repositories. Adding one here
would be a third pattern rather than parity, and the image is not published
anywhere. The question of what happens when it is published is in
[ROADMAP.md](ROADMAP.md).

---

## The defects that changed how this is tested

Each of these shipped, and each changed the shape of the assertions rather than
just the line that was wrong. They are here because the pattern is the point:
every one of them failed **silently**.

### The dependency server had no identity

The pricing server was wrapped in `otelhttp` for tracing but never passed
through the identity middleware, so it emitted the same histogram with both
identity labels empty. On a RED panel that is a nameless row carrying the
dependency's latency beside the real service's. Nothing errored.

Running the stack found it; the test suite did not — because the test served the
dependency **bare**, so it produced no server metrics at all and there was
nothing for the identity assertion to be wrong about.

Two things changed. The middleware moved to `internal/obs` and was exported,
because the rule is not "the API needs this" but "every `otelhttp`-wrapped
server in the process needs this". And the test now wires its dependency exactly
as `cmd/service` wires it, asserting on **every** data point rather than on the
ones that happen to carry the attributes.

**The lesson that stuck:** a test whose wiring differs from production tests the
wiring, not the code.

### The admin API reported a unit it did not accept

`GET /admin/inject` returned nanoseconds under field names ending `_ms`. A
`time.Duration` is an int64 count of nanoseconds, so tagging a raw Duration
`json:"base_latency_ms"` serialises 10ms as 10000000. POST had always read
milliseconds correctly, so the API accepted one unit and reported another, a
factor of a million apart, with no error anywhere.

A test of POST alone passes, because POST was right. A test of GET alone needs
someone to already know the expected number, which is the thing that was wrong.
**Round-tripping is what catches it:** whatever POST accepts, GET must report
back unchanged.

### The code was wrong about its own configuration

The comment on `resource.WithFromEnv` said `OTEL_RESOURCE_ATTRIBUTES` "still
works and wins". It does not win: `resource.New` merges detectors in order and
the last one wins, so the values in `Config` override the environment for the
five keys they share. Keys the environment alone sets do ride along, which is
where the half-truth came from.

This is the way round you want — a service that could be renamed by the
environment it lands in is one whose dashboards move when someone edits a
manifest — but it is the opposite of the usual assumption, which made a comment
asserting the assumption worse than no comment at all. It is asserted over the
exported resource now rather than described.

### The injector was reachable through a public gateway

Not a defect in this repository's code so much as one in the gap between two
repositories, which is the kind this repository exists to talk about.

`/admin/inject` was served on the API listener. Read on its own that is a
documented sharp edge on a demonstration service, and it was documented. Read
alongside the sibling platform, it was a live one: that chart's Service
publishes exactly one port, targeting the container port named `http`, and an
HTTPRoute points a public gateway at it. So the endpoint that sets the error
rate to 1.0 was reachable from outside the cluster by path alone.

Neither repository was wrong about itself. This one said "do not expose this
listener"; the platform said "publish the app's port". The failure was in the
seam, and nothing tested the seam because there is nothing there to test — the
two repositories share a wire protocol and no code.

The fix is a listener boundary rather than a check, so it holds regardless of
what the platform later chooses to publish, and it is asserted in both
directions because moving one route back across the line is a one-word edit
that every other test in the package would still pass.

**The lesson that stuck:** a sharp edge documented in one repository is not
documented at all if a second repository decides the exposure.

### Nothing proved the telemetry left the process

The largest one, and it was an absence rather than a defect. Three tests
asserted on collected metric data through a `ManualReader`, which reads the
SDK's in-memory state. With a `ManualReader` nothing is exported, so an endpoint
that never dials, a resource that never reaches the wire, or a `Shutdown` that
returns before the batch processor has flushed would all pass. The README's
central claim rested on two screenshots.

`internal/obs/otlp_export_test.go` now stands up a real OTLP receiver and
asserts on the bytes a collector would have received: spans carrying the service
identity including the hand-rolled one, a single trace id across the loopback
hop, the duration histogram with identity on every data point, and exemplars
that name a trace which arrived in the same export.

That last assertion is the click in the README, turned into a test. It was
confirmed non-vacuous by breaking each thing it guards in turn — dropping the
`Labeler` write, removing the propagator, switching to `NeverSample`, and
pointing the endpoint at a closed port — and watching it fail differently and
legibly for each.

**The lesson that stuck:** asserting on in-memory SDK state proves the
instrumentation and says nothing at all about the export.
