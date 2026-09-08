# Contributing

## Step 1, before anything else

```sh
make init
```

This sets `core.hooksPath=.githooks` and checks that gitleaks is installed. **It
is required in every clone.** `core.hooksPath` is per-clone configuration:
cloning this repository copies the hook *file* but installs nothing. Until you
run this, the pre-push gate is a file on disk that never executes.

```sh
make test-hook
```

Runs the gate against six kinds of deliberately bad history and prints the
result for each. A gate nobody has watched fail is a gate nobody knows works.

## Single identity

Every commit is authored **and** committed by `Paul Bezilla
<bezilla@protonmail.com>`. Both fields, on every commit, with no exceptions.

Identity is baked into the commit hash, so a wrong one cannot be corrected after
publication without rewriting history. That is why the gate runs before the push
rather than after.

Set it per-clone, never globally:

```sh
git config user.name  'Paul Bezilla'
git config user.email 'bezilla@protonmail.com'
```

## The trailer allowlist

Only three trailer keys may appear in a commit's trailer block, or in an
annotated tag's body. Every other key is refused:

| trailer | rule |
|---|---|
| `Signed-off-by` | must be exactly `Paul Bezilla <bezilla@protonmail.com>` |
| `Verified` | free text |
| `Measured` | free text |

This replaced a name-based denylist over commit messages. Its names were written
in bracket expressions so the file would not contain the strings it matched on.

A denylist can only refuse what somebody already thought to write down, and the
set of keys that do not exist yet cannot be enumerated. An allowlist inverts
that: refusal is on the key, so an unlisted key is refused whether or not the
gate has heard of it.

`make test-hook` proves both directions — that the gate rejects each thing it
claims to, and accepts each thing it claims to.

### The trailer rule has one sharp edge

Whether a `Key: Value` line is a trailer depends on **which paragraph it lands
in**. git parses only the last paragraph, and only when the whole paragraph
parses as trailers. So:

```
Add a thing                          Add a thing

Verified: 3 runs, 0 failures.        Verified: 3 runs, 0 failures.

And a closing paragraph.             ← nothing after it
```

The left-hand message ends in prose, so `Verified:` there is **ordinary text**
and the gate never looks at it. The right-hand message ends with that line, so it
**is** a trailer and its key must be on the allowlist. Same words, same spelling,
two outcomes decided by what comes after.

That is deliberate: it is git's own definition, which is what makes the trailer
block the surface the rule applies to. A `^Key:` regex would be simpler and would
reject ordinary prose — this repository's own messages carry twelve `Key: Value`
shapes that are *not* trailers, including `pins:`, `ships:`, `artefact:`,
`diagram:` and `wanted:`.

If a push is refused for a trailer you thought was prose, check whether it ended
up in the final paragraph. A new evidence word — `Tested:`, `Confirmed:` — needs
adding to the allowlist before it can land there. That is the accepted cost of a
tight list.

### What the allowlist does not catch, on purpose

Two things pass this gate that an earlier version of it would have stopped. Both
are the deliberate reduction, not an oversight.

**Anything in the body of a message.** The gate's scope is the trailer block: it
reads that and nothing else, so a `Key: Value` shape written in a paragraph of
prose is ordinary text and is accepted. Refusing on the key is what makes the
rule hold — an unlisted key is refused whether or not the gate has heard of it,
which a name list cannot promise, because it needs updating every time an
unanticipated name appears. Matching words in prose is a different job, and this
gate does not do it.

**Anything in the working tree.** Nothing greps the checkout.
Hand-written hooks under `.git/hooks/` once did, and `core.hooksPath` makes git
ignore that directory entirely, so any that survive there are inert. They have
not been restored and should not be: it is the same scan, and it walked build
artefacts, so a full validation run could leave a clean tree unpushable.

## Running a scan you can believe

Two rules. Both were learned by getting them wrong during a scan of this
repository's history, and both are about the same thing: a zero is what a clean
repository looks like, and it is also what a broken pipeline looks like.

**Resolve every scan engine to a real path, and invoke it by that path.** Not by
name. On the machine one of these scans ran on, `grep` was a shell function
injected by the surrounding tooling. It answered `--version` with the name of a
PCRE-capable tool and behaved correctly when typed at a prompt, but inside a
`#!/usr/bin/env bash` script it fell through to BSD grep, which has no `-P` at
all. The first two runs of the battery returned an error, then zero, for every
pattern including ones known to match.

```sh
type -a pcre2grep                 # confirm it is a path, not a function or alias
ENGINE=$(command -v pcre2grep)    # resolve once
"$ENGINE" -c -- "$pattern" "$corpus"
```

**Run a canary that must match, before believing any zero — and pick it from the
corpus you are actually scanning.** Not from the repository in general. The
canary's whole job is to prove that this engine, with these flags, reading these
bytes, can still find something; a string that is present somewhere else proves
none of that.

For a scan of the whole repository the canonical identity address does the job,
because it is in this file, in `SECURITY.md` and in the hook:

```sh
"$ENGINE" -c -- 'bezilla@protonmail\.com' CONTRIBUTING.md   # must be non-zero
```

For any narrower corpus, choose again. A scan of one pushed range used that same
address as its canary and got zero — not because the engine was broken, but
because within that range the address occurs only in escaped regex form inside a
code fence, as `protonmail\.com`, so the literal string genuinely was not there.
The canary was wrong for the corpus. Re-canarying against strings that range did
contain (`protonmail`, `gitleaks`, `pcre2grep`, `CONTRIBUTING`) brought it back
to life, and only then were the zeros beside it worth anything.

So: **if the canary returns zero, fix the canary before believing anything
else.** A zero canary is a statement about your setup, never about the corpus.
Establish that the pipeline can find something, then read the zeros. Run a
second, independently implemented engine alongside the first while you are at
it: two engines disagreeing is a finding, and two engines agreeing on a
non-zero canary is what makes their zeros worth reading.

### What gitleaks does not read

gitleaks reports `no leaks found` over bytes it never opened. The two modes skip
for different reasons, so neither one's coverage implies the other's.

**`git` mode** — what the pre-push hook runs, on a laptop and in the `identity`
job — skips
content that produces no text hunk. `git log -p` emits none for a binary file, so
such a commit yields nothing to scan and is not even counted: gitleaks reports
**29** commits where `git rev-list --count HEAD` reported 30, the missing one
being `a6e4c05`, whose entire diff is the two screenshots below.

**`dir` mode** skips by **file extension**, not by content. Proved on identical
bytes: the same 7,730-byte XML scans as 0 bytes named `stack.svg` and as 7,730
bytes named `stack.txt`; conversely PNG bytes named `trace.txt` scan as 48,321.
So `dir` mode reads binary content under an extension it does not exclude, and
refuses text under one it does — `gitleaks dir docs/images/` reports
`scanned ~0 bytes` even though an SVG is sitting in there.

#### The inventory

Established with git's own binary detection (`--numstat` reporting `-`/`-`) over
every commit on every ref, cross-checked against an empty-tree diff at HEAD and a
NUL-byte sniff of all 71 blobs in the object database. All three agree.

| file | size | introduced | `git` mode | `dir` mode |
|---|---|---|---|---|
| `docs/images/exemplar-p99-panel.png` | 27,369 B | `a6e4c05` | not read | not read |
| `docs/images/exemplar-trace.png` | 48,321 B | `a6e4c05` | not read | not read |
| `docs/images/stack.svg` | 7,730 B | `34e4ce2` | **read** | not read (extension) |

Two binary files, both PNGs, both still at HEAD; nothing has ever been deleted or
renamed away, so the historical set and the HEAD set are the same 38 paths. The
SVG is not binary and `git` mode does read it, which is why it is not a history
gap — but it is invisible to `dir` mode, and that is worth knowing before quoting
a `dir` scan as coverage.

#### The pass those files need instead

Nothing is wrong with the tool; there is nothing useful to regex in deflated
pixel data. It just means binary blobs need their own check. Both PNGs have had
it, and both are clean: chunk list `IHDR`/`IDAT`/`IEND` only — no `tEXt`,
`iTXt`, `zTXt`, `eXIf` or `iCCP`, which is where a capture tool writes a
username, a hostname or its own name; trufflehog `filesystem` returning nothing;
and a raw-byte scan through both engines returning zero for addresses, `/Users/` and `/home/` paths, `.internal`/`.corp`/`.lan`, RFC1918
addresses, AWS and GCP key shapes, PEM headers and ARNs.

```sh
# 1. chunk inventory -- anything beyond IHDR/IDAT/IEND deserves reading
python3 -c 'import sys,struct;b=open(sys.argv[1],"rb").read();o=8
while o+8<=len(b):
 n=struct.unpack(">I",b[o:o+4])[0];t=b[o+4:o+8].decode();print(t);o+=12+n
 if t=="IEND":break' docs/images/exemplar-p99-panel.png

# 2. trufflehog, which does read binary
trufflehog filesystem docs/images/ --json --no-update

# 3. the raw bytes through both engines. Canary from THIS corpus, per the rule
#    above -- the identity address is not in a PNG, so it would prove nothing.
"$ENGINE" -c --binary-files=text -- 'IHDR' docs/images/exemplar-trace.png  # non-zero
"$ENGINE" -c -i --binary-files=text -- "$pattern" docs/images/*.png
```

Run all three for any binary that lands here later, and add it to the table.

## All changes land by direct push

Push to `main`, through the hook.

```sh
git push origin main
```

Every server-side merge mode rewrites the author or the committer, so the merge
button is never used here. If you want review before landing, review the branch
locally and then push `main` directly.

## Staging

Stage by explicit path. `git add -A` is how an unreviewed file reaches a commit,
and in this repository an unreviewed file is how something you did not read
reaches published history.

```sh
git status              # every time, before every commit
git add -- path/to/file
```

## Before you push

```sh
make check-all  # everything CI enforces
make identity   # the gate over all of this repository's history
```

`make check` is the fast loop — gofmt, vet, lint, race tests, hook selftest.
`make check-all` adds govulncheck and gitleaks, which are slower and belong
before a push rather than in the edit cycle.

### What CI runs

| job | what it enforces | required |
|---|---|---|
| `build · vet · test` | gofmt, `go vet`, `go build`, `go test -race` | yes |
| `golangci-lint` | the linter set in `.golangci.yml`, pinned to `v2.13.1` | yes |
| `identity` | canonical identity, the trailer allowlist and gitleaks over full history, over all history and all tags, with `fetch-depth: 0` | yes |
| `govulncheck` | advisories on call paths this code actually reaches, pinned to `v1.7.0` | no — see below |

The `identity` job runs the same file the pre-push hook does, in its
`--all-history` mode. The hook is per-clone configuration and does not travel
with a clone; CI is the copy nobody can forget to install. That covers gitleaks
too: the secrets stage is inside the gate file, so the hook's copy is what stops
a secret leaving a laptop and the `identity` job is what scans whether or not
anyone installed the hook.

#### What a green `identity` job means

Three gates, one job, run in order: identity, then trailers, then secrets. Green
means all three passed. **Red names only the first failure.** The script exits on
it, so a job that failed on a wrong committer never reached the secrets stage —
and a red `identity` does not distinguish *gitleaks found something* from
*gitleaks never ran*. Read the log for which stage spoke, rather than treating
the job as a secrets result.

This is the cost of folding the standalone `gitleaks` job in, and it is worth
naming because the reverse reading is the tempting one: the secrets scan is no
longer an independent signal that survives an identity failure. Nothing reaches
`main` unscanned regardless, because `identity` is required and cannot be green
without the secrets stage having run.

#### Required and advisory

`build · vet · test`, `golangci-lint` and `identity` are required checks; branch
protection will not merge without them. `govulncheck` is advisory on purpose.

It resolves its advisory database over the network at run time, so its verdict is
a function of the world on the day it ran, not of this repository. A new advisory
against a path this code already reached turns `main` red with nothing here having
changed — a required check that a third party can fail on your behalf, on a commit
that was green an hour earlier. Pinning the scanner to `v1.7.0` fixes the tool, not
the data it downloads. So it runs on every push and every pull request, and a
finding is a thing to go read rather than a thing that blocks the merge queue.

Secrets do not get that treatment. The gitleaks stage is inside `identity`, which
is required, and it fails closed when the scanner is missing. The asymmetry is
deliberate: a secret in history is a fact about this repository that is true
whenever anyone looks, and the ruleset is carried in the pinned binary rather than
fetched, so the scan cannot change its mind overnight.

### Pins

Actions are pinned by full commit SHA with the version in a trailing comment;
container images are pinned by digest with the tag kept beside it, in both
`deploy/Dockerfile` and `deploy/docker-compose.yml`. Do not replace either with
a floating tag. Renovate watches both and writes a dependency dashboard issue —
it never opens a pull request, because `refs/pull/N/head` is permanent and this
repository does not acquire refs it did not write.

## Code conventions

- Comments explain **why**, not what. If a comment restates the line below it,
  delete one of them.
- The OTel SDK is imported in `internal/obs` and nowhere else. Everything else
  uses the API.
- Tests assert on collected telemetry, not on the absence of an error. Every
  property worth testing here fails silently in production.
- Every non-obvious decision that survived an alternative gets a note about the
  alternative, in the code if it is local, in the README if it is structural,
  and in [DESIGN.md](DESIGN.md) if it beat an alternative worth naming.
- British English, in prose and in comments. `misspell` is set to the UK locale
  and will tell you if you drift.
