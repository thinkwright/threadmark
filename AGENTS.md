# Agent Guide

Repository guidance for AI coding assistants and maintainers working on
Threadmark.

**Last updated:** 2026-05-19

## 1. Project Contract

Threadmark is a shared continuity layer for software developers who hand work
off between Claude Code and Codex in the same repository. It runs beside those
harnesses, observes hook events, writes short perspectival journal entries at
useful boundaries, and gives future sessions a compact startup packet for the
same project.

Threadmark is intentionally narrow. It is not a transcript archive, semantic
search layer, sync service, or general-purpose memory system. Its job is to
preserve enough situated context for independent agents to stay on the same
line of work without inheriting one another's entire sessions.

## 2. Reading Order

Use the public repository files as the source of truth:

1. `README.md` - project summary and user-facing flow.
2. `HOW_IT_WORKS.md` - implementation mechanics and architecture.
3. `QUICKSTART.md` - first-use path and common workflows.
4. `INSTALL.md` - install, upgrade, uninstall, and health checks.
5. Source under `cmd/`, `internal/`, `adapters/`, `prompts/`, and `config/`.
6. Tests near the packages they validate.

If behavior and docs disagree, inspect the implementation and tests, then update
the docs as part of the same change when appropriate.

## 3. Architecture Map

- `cmd/threadmark` - user CLI, hook bridge, install, activation, status, doctor,
  daemon lifecycle, privacy controls, and startup packet surfacing.
- `cmd/threadmarkd` - per-user daemon process.
- `internal/adapters/claudecode` - Claude Code hook translation.
- `internal/adapters/codex` - Codex hook translation.
- `internal/core` - neutral event schema, thread boundaries, trigger
  classification, priority debounce, and checkpoint decisions.
- `internal/daemon` - per-project state, event handling, checkpoint excerpts,
  retry behavior, and daemon-side orchestration.
- `internal/ipc` - Unix-socket newline-delimited JSON transport.
- `internal/journal` - project storage, journal frontmatter, append/read logic,
  and project ID resolution.
- `internal/reflector` - prompt loading, redaction, Claude CLI subprocess calls,
  and reflector recursion guard.
- `adapters/claudecode/hooks` and `adapters/codex/hooks` - shell shims used by
  harness hook configuration.
- `prompts/reflector.md` - default reflector prompt.
- `config/triggers.example.yml` - example trigger configuration.

Runtime shape:

```text
coding session -> threadmark hook -> Unix socket -> threadmarkd
threadmarkd -> core engine -> reflector -> journal store
future SessionStart -> startup packet
```

## 4. Product Vocabulary

Use these terms consistently:

- **sidecar** - Threadmark running beside an agent harness.
- **startup packet** - the complete context artifact surfaced on `SessionStart`.
- **Workspace Snapshot** - generated current git/workspace facts inside the
  startup packet.
- **Project Card** - optional durable project-authored context inside the
  startup packet.
- **Entry N** - selected reflector-written journal entries inside the startup
  packet.

Do not introduce `startup card` or `orientation packet` as product primitives.
Orientation is the goal; `startup packet` is the artifact.

## 5. Scope Boundaries

Implemented scope:

- Go module: `github.com/thinkwright/threadmark`.
- Commands: `threadmark` and `threadmarkd`.
- Supported adapters: Claude Code and Codex.
- Hook events include session start, user prompt, tool use, stop, pre-compact,
  and post-compact surfaces where supported by the harness.
- Reflector backend: Claude CLI subprocess (`claude -p` by default; bare mode is
  available when explicitly configured).
- Startup packets include workspace facts, optional Project Card content, and
  selected recent journal entries.

Out of scope for v0:

- OpenCode and Pi adapters.
- Journal sync across machines.
- Semantic retrieval over the journal.
- Raw transcript or raw tool-output persistence.
- Direct Anthropic SDK integration.
- Durable spooling of checkpoint excerpts.

## 6. Engineering Rules

- Preserve the core/adapter split. Adapter packages translate harness payloads
  into neutral events; shared behavior belongs in `internal/core`,
  `internal/daemon`, `internal/journal`, `internal/ipc`, or
  `internal/reflector`.
- Keep hook shims small and fast. Stateful work belongs in `threadmarkd`.
- Do not turn the CLI into the hot path. Normal continuity should flow through
  hooks, daemon auto-start, checkpoint triggers, journal writes, and
  `SessionStart` startup packets.
- Do not persist raw transcripts, raw tool outputs, or raw hook payloads.
- Redact before reflector calls. Treat redaction as best effort, not a security
  guarantee.
- Do not adopt credential scraping, credential pooling, or programmatic loops
  through interactive-priced subscription auth.
- Do not add the Anthropic SDK or npm/Node dependencies for v0.
- Keep product examples based on a `threadmark` command available on `PATH`, not
  on a maintainer-specific checkout path.
- Do not modify global git config.
- Do not add remotes, change repository visibility, rewrite history, or
  force-push unless a maintainer explicitly asks for that operation.

## 7. Editing Workflow

Before editing:

```sh
git status --short --branch
git log --oneline -10
```

Prefer focused changes that match existing package boundaries. Use `rg` for
search. Respect `.gitignore`; generated artifacts, build output, and
machine-local configuration are not source.

When behavior changes, update the relevant public docs in the same unit of work
when user-facing behavior or contributor expectations change. When shared
behavior changes, add or update tests near the package that owns the contract.

When changing public docs, check for:

- first-user clarity
- sidecar/startup-packet vocabulary consistency
- privacy and model-call boundaries
- install and activation steps that match the current implementation

## 8. Validation

Common checks:

```sh
go test ./...
go vet ./...
git diff --check
```

Use broader checks when risk warrants it:

```sh
go test -race ./...
```

For installed workflows:

```sh
threadmark doctor
threadmark status --last 10
```

Do not rely on `codex exec` to validate Codex hooks; configured project hooks
are validated through the interactive Codex workflow.

## 9. Privacy And Continuity Posture

Threadmark's journal entries are perspectival orientation, not ground truth.
The Workspace Snapshot and Project Card are factual/durable context; journal
entries should be verified against code, tests, git history, and public docs.

Journal mode sends a redacted checkpoint excerpt to the configured reflector
model. Use no-journal mode for sensitive sessions.

Threadmark should remain ambient after setup. Diagnostic, lifecycle, disable,
enable, purge, and status commands are control-plane tools; they should not
become the ordinary user workflow.
