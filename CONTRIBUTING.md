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
make check      # gofmt, vet, race tests, hook selftest
make identity   # the gate over all of this repository's history
```

### What CI runs

| job | what it enforces |
|---|---|
| `build · vet · test` | gofmt, `go vet`, `go build`, `go test -race` |
| `identity` | canonical identity and no attribution strings, over all history, with `fetch-depth: 0` |

The `identity` job runs the same file the pre-push hook does, in its
`--all-history` mode. The hook is per-clone configuration and does not travel
with a clone; CI is the copy nobody can forget to install.

## Code conventions

- Comments explain **why**, not what. If a comment restates the line below it,
  delete one of them.
- The OTel SDK is imported in `internal/obs` and nowhere else. Everything else
  uses the API.
- Tests assert on collected telemetry, not on the absence of an error. Every
  property worth testing here fails silently in production.
- Every non-obvious decision that survived an alternative gets a note about the
  alternative, in the code if it is local and in the README if it is structural.
