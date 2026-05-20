# Security Policy

Threadmark is a local sidecar for AI coding sessions. It observes harness hook
events, keeps per-project state, and can send a redacted checkpoint excerpt to
the configured reflector command.

This document describes the security and privacy posture for the current
pre-release project.

## Reporting Issues

Do not include secrets, private transcripts, exploit details, or sensitive local
paths in a public issue.

If GitHub private vulnerability reporting is enabled for this repository, use
that channel. If it is not available, open a minimal public issue asking for a
private reporting path and include only a short, non-sensitive summary.

Useful reports include:

- a way to reproduce the issue
- affected operating system and Threadmark version
- whether the issue involves hook config, local storage, reflector calls, or
  journal output
- the smallest safe excerpt needed to explain the behavior

## Current Security Model

Threadmark is a local tool. It does not run a hosted service, sync journals
across machines, or provide multi-user access control.

Default local storage is under:

```text
~/.threadmark/
```

The default storage modes are:

- directories: `0700`
- files: `0600`
- Unix socket directory: `0700`
- Unix socket path: `0600`

Project disable markers live in the project checkout:

```text
<project>/.threadmark/disabled
```

## Model-Call Boundary

Journal mode sends a checkpoint excerpt to the configured reflector command.
The default reflector command is the Claude CLI.

Redaction runs before that reflector call. Redaction is best effort; it is not a
security boundary and should not be treated as a guarantee that sensitive
material cannot leave the machine.

Use no-journal mode for sensitive sessions:

```sh
THREADMARK_NO_JOURNAL=true claude
```

or:

```sh
THREADMARK_NO_JOURNAL=true codex
```

If a daemon is already running, restart it with the desired environment before
starting the sensitive session.

## What Threadmark Does Not Intend To Store

Threadmark is designed not to persist:

- raw harness transcripts
- raw hook payloads
- raw tool inputs
- raw tool outputs

Threadmark is also not designed to collect credentials from the environment as a
data source.

Journal entries are short, reflector-written orientation notes. They should be
treated as useful context, not as authoritative records.

## Known Limits

Current redaction covers common token, secret, bearer-token, private-key, and
credential-shaped patterns. It can miss secrets in uncommon formats, encoded
data, screenshots, generated files, arbitrary prose, or tool output summaries
that are already lossy.

Threadmark does not currently provide encryption at rest beyond normal local
filesystem protections. If local disk contents are in scope for a threat model,
use operating-system disk encryption and avoid journal mode for sensitive work.

Threadmark hook shims run inside the agent harness hook path. Keep hook commands
small, local, and reviewable.
