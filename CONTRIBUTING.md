# Contributing

Threadmark is currently a small pre-release Go project. Contributions should
keep the product narrow: local continuity for developers handing work between
Claude Code and Codex in the same repository.

## Project Scope

Threadmark is not a transcript archive, semantic search system, sync service, or
general-purpose memory platform. Please keep changes aligned with the current
v0 boundary unless a maintainer has explicitly accepted a scope expansion.

Current non-goals include:

- raw transcript persistence
- raw hook-payload persistence
- raw tool-output persistence
- cross-machine journal sync
- semantic retrieval over the journal
- hosted services or team coordination servers
- direct Anthropic SDK integration
- npm or Node dependencies

## Development Setup

Use Go 1.24 or newer.

Common local checks:

```sh
go test ./...
go test -race ./...
go vet ./...
git diff --check
```

For shell hook changes, also run:

```sh
git ls-files '*.sh' | xargs -r bash -n
```

Build local binaries with:

```sh
go build -o bin/threadmark ./cmd/threadmark
go build -o bin/threadmarkd ./cmd/threadmarkd
```

Install rebuilt source-checkout binaries with:

```sh
bin/threadmark install
threadmark daemon restart
```

## Release Preparation

Releases are prepared locally before the GitHub release workflow runs. The
workflow is triggered by pushing a `v*` tag, so the source tree must already be
stamped and tagged correctly before that push.

The source-stamped version lives in one file:

```text
internal/buildinfo/version.go
```

Both `threadmark` and `threadmarkd` read that value. Journal frontmatter also
records that value when the daemon writes an entry.

GoReleaser stamps the same variable when it builds release archives:

```text
github.com/thinkwright/threadmark/internal/buildinfo.Version
```

Do not add literal release numbers to README, Quickstart, or install docs unless
the user-facing install flow changes. Those docs should continue to use
`@latest` or "replace with a release tag" wording.

To prepare a release:

```sh
scripts/prepare-release.sh v0.1.0
```

The script requires a clean worktree on `main`, stamps
`internal/buildinfo/version.go`, runs the release checks, commits the stamp, and
creates an annotated local tag. It does not push.

Push in this order:

```sh
git push origin main
# wait for CI on the release commit
git push origin v0.1.0
```

The tag push triggers the GitHub release workflow and GoReleaser.

## Code Guidelines

- Preserve the adapter/core split. Adapter packages translate harness payloads;
  shared behavior belongs in `internal/core`, `internal/daemon`,
  `internal/journal`, `internal/ipc`, or `internal/reflector`.
- Keep hook shims small and fast. Stateful work belongs in `threadmarkd`.
- Do not add raw transcript, raw hook payload, raw tool input, or raw tool
  output persistence.
- Redact before reflector calls. Treat redaction as best effort, not as a
  security guarantee.
- Keep user-facing commands as setup, diagnostic, lifecycle, and privacy
  controls. Normal continuity should remain ambient after activation.
- Prefer focused tests near the package that owns the behavior.

## Documentation Guidelines

Public docs should use the project vocabulary consistently:

- `sidecar`
- `startup packet`
- `Workspace Snapshot`
- `Project Card`
- `Entry N`

Do not introduce `startup card` or `orientation packet` as product primitives.

When behavior changes, update the relevant public docs in the same change.
Public privacy language should be factual and bounded: journal mode sends a
redacted checkpoint excerpt to the configured reflector model, and redaction is
best effort.

## Security And Privacy

See [SECURITY.md](./SECURITY.md) before changing hook ingestion, journal
storage, redaction, reflector calls, purge behavior, or local permissions.

Do not include secrets, private transcripts, or sensitive local paths in issues,
pull requests, tests, fixtures, or docs.
