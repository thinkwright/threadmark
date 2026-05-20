# Threadmark Project Card

canonical_name: Threadmark
module: github.com/thinkwright/threadmark
remote: github.com/thinkwright/threadmark

purpose: Local handoff and continuity for developers moving between Claude Code and Codex in the same repository.

product_thesis: Threadmark preserves perspectival continuity, not semantic memory. It helps a fresh agent inherit the shape of the work without inheriting the whole transcript.

core_runtime: Harness hooks call `threadmark hook <adapter>`, which forwards neutral events over a Unix socket to `threadmarkd`. The daemon owns thread state, trigger/debounce logic, reflector calls, journal writes, and future SessionStart startup packets.

current_harnesses: Claude Code and Codex. Codex uses command hooks; do not validate hooks through `codex exec`.

validation_gates: `go test ./...`, `go test -race ./...`, `go vet ./...`, and `git diff --check`.

continuity_contract: Startup packets should keep journal entries clearly perspectival and non-authoritative. Verify journal context against code, tests, git history, and public docs before relying on it.
