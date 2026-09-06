# Roadmap

What this repository does not do, and what would have to be decided before it
could.

This is a list of open questions, not a schedule. An item is here because the
question is unanswered and interesting; it leaves when it is built, or when it
is ruled out for a reason worth writing down.

---

## Production sampling, and what it costs the demonstration

The service samples every trace. That is correct for watching a dashboard fill
in and wrong for anything with traffic, and the code says so where someone
changing it will read it.

The open question is not *whether* to sample — it is what a reference repository
should show. Head sampling at 1% drops 99% of exemplars with the traces, so the
p99 panel that is the whole point of the repository thins out to nothing and the
walkthrough stops working. Tail sampling in the collector keeps the slow
requests, which is what exemplars are for, but it moves the decision to the
platform side of the boundary this repository exists to draw — and then the
service can no longer demonstrate the behaviour on its own.

There is a real answer here and it probably involves saying both things
explicitly. It has not been written.

## TLS on the OTLP path

`WithInsecure()` is unconditional. There is no code path that dials a collector
over TLS, which means the one thing every real deployment must change is the one
thing the repository does not show. Adding a flag is easy; the question is
whether a reference service should ship a credential-loading path it cannot
demonstrate, or whether the honest move is to keep the local stack plaintext and
document the delta precisely.

## `/admin/inject` on its own listener

The injector shares a listener with `/api/quote`, so anything that can reach the
API can change the latency and error rate of every request after it. On a laptop
that is fine. Deployed through the sibling platform's paved-road chart, the API
port is published through a route and the admin path is not separately blocked.

The fix is not complicated — a third listener on a port that is not in the
Service, the way the pricing dependency is already handled — but it changes the
`make slow` / `make errors` workflow that the README's walkthrough depends on,
and a demonstration nobody can drive is worse than one with a documented sharp
edge. Written down in [SECURITY.md](SECURITY.md) in the meantime.

## Logs over OTLP

Currently out of scope on purpose: stdout is what a container runtime already
collects, and `trace_id` on the line is the correlation that matters. The open
question is whether a repository claiming to demonstrate the three signals can
honestly demonstrate two of them and argue about the third, or whether the OTLP
logs path should exist behind a flag so the argument is a choice a reader can
run rather than a paragraph they have to accept.

## Publishing an image, and what has to be true first

Nothing publishes the image. It is built by the compose stack and side-loaded
into a kind node by the sibling platform, so there is no registry, no tag and no
release.

If it is ever published, three things arrive together and none of them is
interesting alone: a versioned tag, a scan that fails on HIGH and CRITICAL —
`trivy` or `grype`, and none of the sibling Go repositories has picked one yet —
and an SBOM or a build attestation, which is the only one of the three that says
anything a scan does not. Adding a scanner before there is an artifact to scan
would be a gate on nothing.

## An end-to-end test against a real collector

The export test asserts on the bytes this service puts on the wire, and stops
there deliberately. What it does not cover is the second half of the pipeline:
that the collector's remote-write translator turns those exemplars into
Prometheus samples with a `trace_id` label, that Prometheus stores them, and
that the Grafana data source links them to a trace that Jaeger can serve.

Every one of those hops has a documented silent failure in the README, and none
of them is tested. The obstacle is honest rather than technical: that pipeline
belongs to the platform repository, and a test of it here would be this
repository asserting on somebody else's configuration. The interesting version
of this question is which side of the boundary such a test should live on.

## Semantic convention drift

The platform's recording rules query `http_server_request_duration_seconds_*`.
That name is not chosen here — it is the semantic convention plus the unit
suffix the collector appends — and it has changed upstream before. There is a
test asserting the metric name, which catches the change on the next dependency
bump. What there is not is any statement of what the repository does when it
happens: whether it follows upstream immediately and breaks the platform's
rules, or holds and documents the gap. That is a decision about a contract
between two repositories, and it has not been made.
