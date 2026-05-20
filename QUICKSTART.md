# Threadmark Quickstart

Threadmark is a shared continuity layer for software developers who hand work
off between Claude Code and Codex in the same repository.

Threadmark runs beside both harnesses. Once installed, you keep using Claude
Code and Codex normally. Threadmark receives live work events through harness
hooks, keeps per-project state, accumulates events while work is active, holds a
pending checkpoint until the work reaches a useful boundary, writes journal
entries at those boundaries, and injects a startup packet into future sessions
for the same project.

## Fit Check

Threadmark is useful when coding work spans more than one session, harness,
compaction, branch, or handoff. It is especially useful when you move between
Claude Code and Codex on the same repo and need the next agent to understand the
same line of work. It is less useful when a task fits in one short session and
the harness's own resume feature keeps enough context.

Threadmark should be ambient after setup. The commands below are setup, diagnostics, and privacy controls. They are not intended to become a daily workflow.

## Requirements

- macOS or Linux
- Homebrew, or Go 1.24 or newer for the `go install` path
- Claude Code if you want Claude Code hooks
- Codex if you want Codex hooks
- A working `claude -p` command if you want journal writes

The reflector uses the Claude CLI. Codex can produce events and read startup packets, but journal reflection goes through `claude -p`.

## Install

Install the CLI and daemon with Homebrew:

```sh
brew install --cask thinkwright/tap/threadmark
```

No daemon command is needed during normal setup.

If you prefer `go install`:

```sh
go install github.com/thinkwright/threadmark/cmd/threadmark@latest github.com/thinkwright/threadmark/cmd/threadmarkd@latest
```

The Go path installs into Go's command directory:

```text
$(go env GOBIN) if set
$(go env GOPATH)/bin otherwise, usually $HOME/go/bin
```

Make sure that directory is on the PATH used by the shell that launches
`claude` or `codex`. If you have not customized `GOBIN` and `threadmark` is not
found, add Go's default bin directory:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

The binaries include a default reflector prompt, so no prompt-path environment variable is required for normal use.

See [INSTALL.md](./INSTALL.md) for source checkout installation, upgrades, uninstall, and health checks.

## Activate A Project

Go to the project you want Threadmark to observe:

```sh
cd ~/code/my-project
```

Activate the project:

```sh
threadmark activate
```

This installs the default project-local hooks for Claude Code and Codex and
makes the per-user daemon ready:

```text
.claude/settings.json
.codex/hooks.json
```

For Codex project hooks, open Codex in the project and run `/hooks` once to review and trust the installed command hooks.

## Start Working

Start your harness normally:

```sh
claude
```

or:

```sh
codex
```

On the first hook event, `threadmark hook ...` forwards the event to
`threadmarkd`. `threadmark activate` already starts the daemon during setup;
hook auto-start is the fallback if the daemon has stopped or the machine has
restarted.

## Check Health

Run:

```sh
threadmark doctor
```

Doctor checks local storage permissions, daemon reachability, project state,
journal presence, hook config, reflector mode, reflector command, and reflector
prompt.

A fresh project may have no state or journal yet. That is normal until hook
events arrive and a checkpoint fires. The important setup signal is `0 fail`.

## Advanced Activation

Launchers and automation can use quiet mode:

```sh
threadmark activate --quiet
```

You can install user-level hooks instead of project-local hooks:

```sh
threadmark activate --scope user
```

Lower-level harness-specific init commands are available for custom setups:

```sh
threadmark init claude-code
threadmark init codex
```

## What Happens While You Work

Threadmark records neutral events from the harness:

- user prompts
- assistant stop events
- tool calls and summarized tool results
- file changes
- git commits detected from tool activity
- compaction events

The daemon classifies checkpoint triggers:

- `/threadmark:checkpoint` or `/checkpoint`
- git commit
- session stop after substantive work
- idle gap after activity
- safety net after many turns
- pre-compaction event

Most triggers wait through a short debounce window so several related events can become one journal entry. During active work, Threadmark accumulates events and may show a `pending_trigger` in `threadmark status`; that is normal, not a stalled journal. Pre-compaction fires immediately because the harness is about to rewrite its working context.

Lifecycle-only sessions are suppressed. Starting and exiting an agent without substantive work should not produce a journal entry.

## What a New Session Receives

On `SessionStart`, Threadmark writes a short startup packet to the harness.

Terminology: the `startup packet` is the whole SessionStart artifact. `Workspace Snapshot`, `Project Card`, and selected `Entry` sections are parts of it. There is no separate "startup card" primitive.

It begins with current git facts:

```text
## Workspace Snapshot

working_dir: /home/you/code/my-project
git_root: /home/you/code/my-project
branch: main
head: 4f3a91c
status: dirty (2 paths)
dirty_path: M src/session.ts
dirty_path: ?? notes/repro.md
```

Then it may include an optional Project Card if one exists:

```text
## Project Card

source: THREADMARK.md

canonical_name: Threadmark
purpose: Local handoff and continuity for coding agents.
validation_gates: go test ./...; go vet ./...; git diff --check
```

Threadmark checks `THREADMARK_PROJECT_CARD`, `.threadmark/project-card.md`, and `THREADMARK.md` at the git root. It reads the card but does not write or maintain it.

Then it includes selected recent journal entries:

```text
Selected 2 of 12 recent journal entries; omitted 4 obvious low-signal/no-op entries. Most recent selected entry is first.

## Entry 1

source: e_18b0... | 2026-05-18T16:14:50Z | claude-code | stop

> I was trying to stabilize the hook path, not redesign the adapter.

The useful bit: the no-journal smoke passed.
The brittle bit: daemon reuse can hide environment changes.
Start by checking the current daemon mode before changing code.
```

The Workspace Snapshot is factual local context. The Project Card is durable project-authored context. Journal entries are perspective, not truth. They are meant to orient the arriving agent, not replace git history, tests, or project docs.

Threadmark scans a recent window of entries and omits entries that explicitly label themselves as low-signal, such as "nothing to hand off" or "plumbing artifact." It does not perform semantic retrieval or ranking.

## Read the Journal

Print the current project's recent entries:

```sh
threadmark journal
```

Print one entry:

```sh
threadmark journal --last 1
```

Use another working directory:

```sh
threadmark journal --working-dir /path/to/project --last 3
```

## Inspect Local State

Show daemon, project, state, journal, and recent log status:

```sh
threadmark status
```

Show more recent daemon log lines:

```sh
threadmark status --last 50
```

Machine-readable output:

```sh
threadmark status --json
```

Follow the daemon log:

```sh
threadmark status --follow
```

These commands are diagnostic tools. The normal continuity path is hooks plus startup packets.

## Daemon Controls

You normally do not need daemon lifecycle commands. `threadmark activate` makes
the daemon ready during setup, and hooks auto-start it later if needed. Use
these commands for troubleshooting, upgrades, or source-checkout development.

Manually start the daemon:

```sh
threadmark daemon start
```

Stop it:

```sh
threadmark daemon stop
```

Restart after rebuilding `threadmarkd`:

```sh
threadmark daemon restart
```

Check daemon status:

```sh
threadmark daemon status
```

If you rebuilt from source and want running sessions to use the new daemon:

```sh
bin/threadmark install
threadmark daemon restart
```

## Journal-Off Testing

For falsification or sensitive work, run without reflector calls or journal writes:

```sh
THREADMARK_NO_JOURNAL=true claude
```

or:

```sh
THREADMARK_NO_JOURNAL=true codex
```

This affects a daemon spawned by that command. If a daemon is already running, stop it first or restart it with the desired environment.

In no-journal mode, Threadmark still observes events and logs trigger decisions, but checkpoints are recorded as skipped instead of being reflected into the journal.

## Disable or Re-Enable a Project

Disable Threadmark for the current project:

```sh
threadmark disable
```

This creates:

```text
<project>/.threadmark/disabled
```

Hooks check this marker and exit without forwarding events.

Re-enable:

```sh
threadmark enable
```

## Purge Local Storage

Remove Threadmark storage for the current project:

```sh
threadmark purge --project
```

Remove all Threadmark storage under the configured root:

```sh
threadmark purge --all
```

Add `--yes` to skip the confirmation prompt in scripts:

```sh
threadmark purge --project --yes
```

## Storage Layout

Threadmark stores user-local data under:

```text
~/.threadmark/
  daemon.sock
  daemon.pid
  daemon.log
  projects/
    <project-hash>/
      state.json
      journal.md
```

Defaults:

- directories are created with `0700`
- files are written with `0600`
- raw tool outputs are not persisted
- raw hook payloads are not stored in the journal
- reflector input is redacted before the model call
- redaction is best effort, not a security guarantee

Journal mode sends the best-effort redacted checkpoint excerpt to the configured
reflector model. Use no-journal mode for sensitive sessions.

## Reflector Prompt

The installed daemon includes the default reflector prompt. Journal frontmatter records the prompt hash so later debugging can tell which prompt produced an entry.

There is no `threadmark prompt edit` or `threadmark config set` command yet. For now, source-checkout prompt editing is a developer workflow: edit the prompt file and start or restart the daemon with an absolute prompt path.

Examples:

```sh
THREADMARKD_ARGS="--reflector-prompt /absolute/path/to/threadmark/prompts/reflector.md" threadmark daemon restart
```

```sh
export THREADMARK_REFLECTOR_MODE=bare
```

## Troubleshooting

Hooks are installed but nothing appears:

```sh
threadmark doctor
threadmark status --last 50
```

Codex hooks do not run:

```text
Run /hooks in Codex and trust the project hooks.
```

Journal entries are not written:

```sh
threadmark status --last 50
```

Look for log entries such as:

- `trigger.candidate`
- `trigger.fired`
- `reflector.started`
- `reflector.failed`
- `journal.skipped`
- `journal.write_failed`

You rebuilt the daemon:

```sh
bin/threadmark install
threadmark daemon restart
```

## Limits

- Threadmark is distributed as Go commands; install with Homebrew, `go install`,
  or build from source.
- The reflector backend is Claude CLI only.
- Journal mode sends a best-effort redacted checkpoint excerpt to the configured reflector model; use `--no-journal` for sensitive sessions.
- Codex hooks require explicit `/hooks` trust for project-local hooks.
- OpenCode and Pi adapters are not included.
- Cross-machine journal sync is not implemented.
- Semantic retrieval over journal entries is not implemented.
- Slash commands are recognized only as normal user text patterns; Threadmark does not install harness-native slash commands yet.
