# Threadmark Project Card

canonical_name: Threadmark
module: github.com/thinkwright/threadmark
remote: github.com/thinkwright/threadmark

purpose: Local handoff and continuity for Claude Code, Codex, and AI coding agents.

product_thesis: Threadmark preserves perspectival continuity, not semantic memory. It should help a fresh agent inherit the shape of the work without inheriting the whole transcript.

core_runtime: Harness hooks call `threadmark hook <adapter>`, which forwards neutral events over a Unix socket to `threadmarkd`. The daemon owns thread state, trigger/debounce logic, reflector calls, journal writes, and future SessionStart packets.

current_harnesses: Claude Code and Codex. Codex uses command hooks; do not validate hooks through `codex exec`.

validation_gates: `go test ./...`, `go test -race ./...`, `go vet ./...`, and `git diff --check`.

dogfood_goal: Startup packets should reduce dependence on manual recovery notes while keeping journal entries clearly perspectival and non-authoritative.
