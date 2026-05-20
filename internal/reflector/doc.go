// Package reflector invokes the configured harness CLI subprocess to generate
// the perspectival body of each journal entry. Convenience mode calls Claude
// Code with hooks disabled for the subprocess, while bare mode uses
// `claude --bare -p`. The default reflector prompt is embedded into the
// daemon, and source checkouts can override it with prompts/reflector.md or an
// explicit prompt path.
package reflector
