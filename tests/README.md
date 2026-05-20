# Tests

Unit tests colocate with their packages (Go convention: `foo.go` and `foo_test.go` in the same directory).

This directory holds **integration tests** that exercise the daemon end-to-end:

- IPC roundtrip (shim → unix socket → daemon → event processed)
- Trigger classifier behavior on realistic event sequences
- Debounce window behavior under concurrent trigger fires
- Reflector subprocess invocation (with `claude -p --bare` mocked)
- Journal append and last-N read across thread boundaries

Manual smoke helpers:

- [`claude-smoke.md`](./claude-smoke.md) documents `scripts/claude-smoke.sh`, which automates build/install/synthetic smoke and can launch Claude Code for the no-journal live test.
