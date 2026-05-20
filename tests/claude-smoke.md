# Claude Code Smoke Test

Use the script from the repo root:

```sh
scripts/claude-smoke.sh --launch --reset-root
```

The script automates:

- building `bin/threadmark` and `bin/threadmarkd`
- installing project-level Claude Code hooks in `.claude/settings.json`
- running a synthetic no-journal hook/daemon smoke test
- launching Claude Code with:
  - `THREADMARK_BIN`
  - `THREADMARKD_BIN`
  - `THREADMARK_ROOT`
  - `THREADMARK_NO_JOURNAL=true`
  - fast test debounce/tick flags via `THREADMARKD_ARGS`
- checking `~/.threadmark/daemon.log` after Claude exits

Inside Claude Code, send one small prompt:

```text
Please say "Threadmark smoke test started", then stop.
```

Expected log kinds after Claude exits:

- `event.received`
- `trigger.candidate`
- `trigger.fired`
- `journal.skipped`

`journal.skipped` is expected for the first smoke test because `THREADMARK_NO_JOURNAL=true`.

To only print the Claude Code settings fragment:

```sh
scripts/claude-smoke.sh --print-settings
```

To run the same flow with live reflector-backed journal writes after no-journal passes:

```sh
scripts/claude-smoke.sh --live-reflector
```
