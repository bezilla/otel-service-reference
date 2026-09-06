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

## No attribution marks, of any kind

Nothing in this repository references any AI assistant or vendor: not in commit
messages, trailers, code, comments, documentation, filenames, branch names, tag
objects, configuration or CI. No co-authorship trailers. No generated-by notes.
No session links.

The gate enforces this over the commit message *and* the tree at every commit in
the push range — not just the tip — so a term cannot land in one commit and be
deleted by a later one in the same push.

The forbidden terms are written in the hook as single-character bracket
expressions, so the file bans terms it does not itself contain and can therefore
scan a tree that includes it. `make test-hook` verifies the patterns still match
their literals; do not "simplify" the brackets away.

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

gitleaks skips binary files, in `git` mode and in `dir` mode alike, and reports
`no leaks found` over bytes it never opened. It is visible in the commit count:
gitleaks reports **29** where `git rev-list --count HEAD` reports 30, because
`a6e4c05` changes only the two screenshots under `docs/images/` and so produces
no scannable fragment for the counter to see. `gitleaks dir docs/images/`
reports `scanned ~0 bytes`.

Nothing is wrong with the tool -- there is nothing useful to regex in deflated
pixel data. The consequence is that binary blobs are outside its coverage and
need their own pass, which is two checks:

```sh
# 1. the PNG text chunks, where a capture tool writes a username or a software
#    name. These files carry IHDR, IDAT and IEND only, and no tEXt/iTXt/zTXt.
python3 -c 'import sys,struct;b=open(sys.argv[1],"rb").read();o=8
while o+8<=len(b):
 n=struct.unpack(">I",b[o:o+4])[0];t=b[o+4:o+8].decode();print(t);o+=12+n
 if t=="IEND":break' docs/images/exemplar-p99-panel.png

# 2. the raw bytes, through the engines above, with the same canary discipline
"$ENGINE" -c -i --binary-files=text -- 'bezilla@protonmail\.com' docs/images/*.png
```

Add the same pass for any binary that lands here later.

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
and in this repository an unreviewed file is how a forbidden string reaches
published history.

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

| job | what it enforces |
|---|---|
| `build · vet · test` | gofmt, `go vet`, `go build`, `go test -race` |
| `golangci-lint` | the linter set in `.golangci.yml`, pinned to `v2.13.1` |
| `govulncheck` | advisories on call paths this code actually reaches, pinned to `v1.7.0` |
| `identity` | canonical identity and no attribution strings, over all history, with `fetch-depth: 0` |
| `gitleaks` | secrets, over full history, with `fetch-depth: 0` |

The `identity` job runs the same file the pre-push hook does, in its
`--all-history` mode. The hook is per-clone configuration and does not travel
with a clone; CI is the copy nobody can forget to install. gitleaks runs in both
places on purpose: the hook's copy is what stops a secret leaving a laptop, and
the job is what runs whether or not anyone installed the hook.

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
