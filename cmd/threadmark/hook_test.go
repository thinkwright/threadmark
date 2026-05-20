package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
	"github.com/thinkwright/threadmark/internal/ipc"
	tmjournal "github.com/thinkwright/threadmark/internal/journal"
	"github.com/thinkwright/threadmark/internal/reflector"
)

func TestHookClaudeCodeSendsTranslatedEvent(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	root := filepath.Join(t.TempDir(), ".threadmark")
	received := startTestIPCServer(t, socketPath, 1)

	input := `{
		"session_id": "abc123",
		"transcript_path": "/tmp/transcript.jsonl",
		"cwd": "` + escapeJSON(t.TempDir()) + `",
		"permission_mode": "default",
		"hook_event_name": "UserPromptSubmit",
		"prompt": "Keep going carefully"
	}`
	var stdout, stderr bytes.Buffer
	code := hook([]string{
		"claude-code",
		"--root", root,
		"--socket", socketPath,
		"--no-spawn",
		"--strict",
	}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook exit = %d stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}

	event := <-received
	if event.Kind != core.KindText || event.Harness != "claude-code" || event.TranscriptID != "abc123" {
		t.Fatalf("event = %#v", event)
	}
	payload := decodeCorePayload[core.TextPayload](t, event.Payload)
	if payload.Actor != core.ActorUser || payload.Content != "Keep going carefully" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestHookCodexSendsTranslatedEvent(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	root := filepath.Join(t.TempDir(), ".threadmark")
	received := startTestIPCServer(t, socketPath, 1)

	input := `{
		"session_id": "codex-session",
		"transcript_path": "/tmp/transcript.jsonl",
		"cwd": "` + escapeJSON(t.TempDir()) + `",
		"permission_mode": "default",
		"hook_event_name": "UserPromptSubmit",
		"turn_id": "turn-1",
		"prompt": "Keep going carefully"
	}`
	var stdout, stderr bytes.Buffer
	code := hook([]string{
		"codex",
		"--root", root,
		"--socket", socketPath,
		"--no-spawn",
		"--strict",
	}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook exit = %d stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}

	event := <-received
	if event.Kind != core.KindText || event.Harness != "codex" || event.TranscriptID != "codex-session" {
		t.Fatalf("event = %#v", event)
	}
	payload := decodeCorePayload[core.TextPayload](t, event.Payload)
	if payload.Actor != core.ActorUser || payload.Content != "Keep going carefully" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestHookClaudeCodeSessionStartOutputsJournalContext(t *testing.T) {
	workingDir := t.TempDir()
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	root := filepath.Join(t.TempDir(), ".threadmark")
	received := startTestIPCServer(t, socketPath, 1)
	writeTestJournalEntry(t, root, workingDir)

	input := `{
		"session_id": "abc123",
		"transcript_path": "/tmp/transcript.jsonl",
		"cwd": "` + escapeJSON(workingDir) + `",
		"hook_event_name": "SessionStart",
		"source": "startup",
		"model": "claude-sonnet-4-6"
	}`
	var stdout, stderr bytes.Buffer
	code := hook([]string{
		"claude-code",
		"--root", root,
		"--socket", socketPath,
		"--no-spawn",
		"--strict",
	}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook exit = %d stderr=%s", code, stderr.String())
	}
	event := <-received
	if event.Kind != core.KindText {
		t.Fatalf("event kind = %s, want text", event.Kind)
	}

	var output struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("unmarshal stdout: %v\nstdout=%s", err, stdout.String())
	}
	if output.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName = %q", output.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(output.HookSpecificOutput.AdditionalContext, sessionContextPreamble) {
		t.Fatalf("context missing preamble: %s", output.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(output.HookSpecificOutput.AdditionalContext, "I learned the adapter path.") {
		t.Fatalf("context missing journal body: %s", output.HookSpecificOutput.AdditionalContext)
	}
}

func TestHookCodexSessionStartOutputsJournalContext(t *testing.T) {
	workingDir := t.TempDir()
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	root := filepath.Join(t.TempDir(), ".threadmark")
	received := startTestIPCServer(t, socketPath, 1)
	writeTestJournalEntry(t, root, workingDir)

	input := `{
		"session_id": "codex-session",
		"transcript_path": "/tmp/transcript.jsonl",
		"cwd": "` + escapeJSON(workingDir) + `",
		"hook_event_name": "SessionStart",
		"source": "startup",
		"model": "gpt-5.5"
	}`
	var stdout, stderr bytes.Buffer
	code := hook([]string{
		"codex",
		"--root", root,
		"--socket", socketPath,
		"--no-spawn",
		"--strict",
	}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook exit = %d stderr=%s", code, stderr.String())
	}
	event := <-received
	if event.Kind != core.KindText || event.Harness != "codex" {
		t.Fatalf("event = %#v", event)
	}

	var output struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("unmarshal stdout: %v\nstdout=%s", err, stdout.String())
	}
	if output.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName = %q", output.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(output.HookSpecificOutput.AdditionalContext, sessionContextPreamble) {
		t.Fatalf("context missing preamble: %s", output.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(output.HookSpecificOutput.AdditionalContext, "I learned the adapter path.") {
		t.Fatalf("context missing journal body: %s", output.HookSpecificOutput.AdditionalContext)
	}
}

func TestHookSessionStartOutputsContextWhenDaemonUnavailable(t *testing.T) {
	workingDir := t.TempDir()
	socketPath := filepath.Join(t.TempDir(), "missing.sock")
	root := filepath.Join(t.TempDir(), ".threadmark")
	writeTestJournalEntry(t, root, workingDir)

	input := `{
		"session_id": "abc123",
		"transcript_path": "/tmp/transcript.jsonl",
		"cwd": "` + escapeJSON(workingDir) + `",
		"hook_event_name": "SessionStart",
		"source": "startup"
	}`
	var stdout, stderr bytes.Buffer
	code := hook([]string{
		"claude-code",
		"--root", root,
		"--socket", socketPath,
		"--no-spawn",
		"--strict",
	}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook exit = %d stderr=%s", code, stderr.String())
	}

	var output struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("unmarshal stdout: %v\nstdout=%s", err, stdout.String())
	}
	if output.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName = %q", output.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(output.HookSpecificOutput.AdditionalContext, "I learned the adapter path.") {
		t.Fatalf("context missing journal body: %s", output.HookSpecificOutput.AdditionalContext)
	}
	hookLog, err := os.ReadFile(filepath.Join(root, "hook.log"))
	if err != nil {
		t.Fatalf("read hook log: %v", err)
	}
	if !strings.Contains(string(hookLog), "dial threadmark daemon") {
		t.Fatalf("hook log missing daemon failure: %s", string(hookLog))
	}
}

func TestSessionContextSkipsSelfDeclaredLowSignalEntries(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(t.TempDir(), ".threadmark")
	writeTestJournalEntryWithBody(t, root, workingDir, "Useful prior work", "I fixed the brittle startup packet path and need to validate SessionStart.")
	writeTestJournalEntryWithBody(t, root, workingDir, "Plumbing artifact", "This entry is a plumbing artifact, not a work record. There is nothing to hand off.")

	contextText, err := sessionContext(root, workingDir, 1, 2)
	if err != nil {
		t.Fatalf("sessionContext returned error: %v", err)
	}
	for _, want := range []string{
		sessionContextPreamble,
		"Selected 1 of 2 recent journal entries; omitted 1 obvious low-signal/no-op entries.",
		"source: e_",
		"claude-code | stop",
		"Useful prior work",
		"I fixed the brittle startup packet path",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context missing %q:\n%s", want, contextText)
		}
	}
	if strings.Contains(contextText, "Plumbing artifact") || strings.Contains(contextText, "nothing to hand off") {
		t.Fatalf("context included low-signal entry:\n%s", contextText)
	}
	for _, notWant := range []string{
		"entry_id:",
		"schema_version:",
		"reflector_prompt_hash:",
	} {
		if strings.Contains(contextText, notWant) {
			t.Fatalf("context included frontmatter %q:\n%s", notWant, contextText)
		}
	}
}

func TestSessionContextIncludesWorkspaceSnapshotWithoutJournal(t *testing.T) {
	workingDir := makeTestGitRepo(t)
	root := filepath.Join(t.TempDir(), ".threadmark")

	contextText, err := sessionContext(root, workingDir, 3, 12)
	if err != nil {
		t.Fatalf("sessionContext returned error: %v", err)
	}
	for _, want := range []string{
		sessionContextPreamble,
		"## Workspace Snapshot",
		"working_dir: " + workingDir,
		"git_root: " + workingDir,
		"branch: main",
		"head:",
		"status: clean",
		"No Threadmark journal entries found for this project yet.",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context missing %q:\n%s", want, contextText)
		}
	}
}

func TestSessionContextIncludesProjectCardBeforeJournalEntries(t *testing.T) {
	workingDir := makeTestGitRepo(t)
	root := filepath.Join(t.TempDir(), ".threadmark")
	cardPath := filepath.Join(workingDir, "THREADMARK.md")
	card := "# Threadmark Project Card\n\ncanonical_name: Threadmark\npurpose: Local handoff and continuity for coding agents.\n"
	if err := os.WriteFile(cardPath, []byte(card), tmjournal.FileMode); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	writeTestJournalEntry(t, root, workingDir)

	contextText, err := sessionContext(root, workingDir, 1, 1)
	if err != nil {
		t.Fatalf("sessionContext returned error: %v", err)
	}
	for _, want := range []string{
		"## Workspace Snapshot",
		"## Project Card",
		"source: THREADMARK.md",
		"canonical_name: Threadmark",
		"Selected 1 of 1 recent journal entries.",
		"I learned the adapter path.",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context missing %q:\n%s", want, contextText)
		}
	}
	assertBefore(t, contextText, "## Workspace Snapshot", "## Project Card")
	assertBefore(t, contextText, "## Project Card", "Selected 1 of 1 recent journal entries.")
}

func TestSessionContextUsesProjectCardEnvOverride(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(t.TempDir(), ".threadmark")
	cardPath := filepath.Join(t.TempDir(), "card.md")
	if err := os.WriteFile(cardPath, []byte("env card TOKEN=abc12345678901234567890\n"), tmjournal.FileMode); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv(projectCardEnv, cardPath)

	contextText, err := sessionContext(root, workingDir, 3, 12)
	if err != nil {
		t.Fatalf("sessionContext returned error: %v", err)
	}
	for _, want := range []string{
		"## Project Card",
		"source: " + projectCardEnv + "=" + cardPath,
		"env card TOKEN=<redacted>",
		"No Threadmark journal entries found for this project yet.",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context missing %q:\n%s", want, contextText)
		}
	}
}

func TestWorkspaceSnapshotShowsDirtyPaths(t *testing.T) {
	workingDir := makeTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(workingDir, "changed.txt"), []byte("dirty\n"), tmjournal.FileMode); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	snapshot := workspaceSnapshot(workingDir)
	for _, want := range []string{
		"## Workspace Snapshot",
		"branch: main",
		"status: dirty (1 paths)",
		"dirty_path: ?? changed.txt",
	} {
		if !strings.Contains(snapshot, want) {
			t.Fatalf("snapshot missing %q:\n%s", want, snapshot)
		}
	}
}

func assertBefore(t *testing.T, text, first, second string) {
	t.Helper()
	firstIdx := strings.Index(text, first)
	secondIdx := strings.Index(text, second)
	if firstIdx < 0 || secondIdx < 0 || firstIdx >= secondIdx {
		t.Fatalf("expected %q before %q in:\n%s", first, second, text)
	}
}

func TestFormatStartupEntryFallsBackForNonJournalText(t *testing.T) {
	input := "plain startup note"
	if got := formatStartupEntry(input); got != input {
		t.Fatalf("formatStartupEntry = %q, want %q", got, input)
	}
}

func TestHookClaudeCodeSkipsDuringReflectorSubprocess(t *testing.T) {
	t.Setenv(reflector.ReflectorActiveEnv, "1")

	for _, adapter := range []string{"claude-code", "codex"} {
		t.Run(adapter, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := hook([]string{
				adapter,
				"--strict",
			}, strings.NewReader("{not json"), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("hook exit = %d stderr=%s", code, stderr.String())
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
			}
		})
	}
}

func TestSessionContextFallsBackWhenAllEntriesLookLowSignal(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(t.TempDir(), ".threadmark")
	writeTestJournalEntryWithBody(t, root, workingDir, "First plumbing artifact", "This entry is a plumbing artifact. There is nothing to hand off.")
	writeTestJournalEntryWithBody(t, root, workingDir, "Latest plumbing artifact", "This entry is also a plumbing artifact. There is nothing to hand off.")

	contextText, err := sessionContext(root, workingDir, 1, 2)
	if err != nil {
		t.Fatalf("sessionContext returned error: %v", err)
	}
	for _, want := range []string{
		"Selected 1 of 2 recent journal entries; omitted 1 obvious low-signal/no-op entries.",
		"Latest plumbing artifact",
		"nothing to hand off",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context missing %q:\n%s", want, contextText)
		}
	}
	if strings.Contains(contextText, "First plumbing artifact") {
		t.Fatalf("context included older low-signal fallback entry:\n%s", contextText)
	}
}

func startTestIPCServer(t *testing.T, socketPath string, want int) <-chan core.Event {
	t.Helper()
	received := make(chan core.Event, want)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server := ipc.Server{
		SocketPath: socketPath,
		Handler: ipc.HandlerFunc(func(_ context.Context, event core.Event) error {
			received <- event
			return nil
		}),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("ListenAndServe returned error: %v", err)
		}
	})
	waitForTestSocket(t, socketPath)
	return received
}

func waitForTestSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 10*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for socket %s", socketPath)
}

func writeTestJournalEntry(t *testing.T, root, workingDir string) {
	t.Helper()
	writeTestJournalEntryWithBody(t, root, workingDir, "Adapter path learned", "I learned the adapter path.")
}

func writeTestJournalEntryWithBody(t *testing.T, root, workingDir, summary, body string) {
	t.Helper()
	ts := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	entryID, err := tmjournal.NewEntryID(ts)
	if err != nil {
		t.Fatalf("NewEntryID returned error: %v", err)
	}
	entry := tmjournal.Entry{
		Frontmatter: tmjournal.Frontmatter{
			EntryID:              entryID,
			ThreadID:             "t_test",
			TranscriptID:         "abc123",
			Trigger:              "stop",
			Timestamp:            ts,
			SourceEventRange:     []string{"evt_1", "evt_2"},
			SourceStartedAt:      ts.Add(-time.Minute),
			SourceEndedAt:        ts,
			Harness:              "claude-code",
			WorkingDir:           workingDir,
			ReflectorBackend:     "claude-code",
			ReflectorCommandMode: "convenience",
			ReflectorModel:       "claude-cli-default",
			ReflectorPromptHash:  "sha256:test",
			ThreadmarkVersion:    "test",
		},
		Body: fmt.Sprintf("> %s.\n\n%s", summary, body),
	}
	if _, err := tmjournal.NewStore(root).Append(workingDir, entry); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
}

func makeTestGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runTestGit(t, dir, "init")
	runTestGit(t, dir, "checkout", "-b", "main")
	runTestGit(t, dir, "config", "user.name", "Threadmark Test")
	runTestGit(t, dir, "config", "user.email", "threadmark@example.test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), tmjournal.FileMode); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	runTestGit(t, dir, "add", "README.md")
	runTestGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
}

func decodeCorePayload[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	payload, err := core.DecodePayload[T](raw)
	if err != nil {
		t.Fatalf("DecodePayload returned error: %v", err)
	}
	return payload
}

func escapeJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return strings.Trim(string(encoded), `"`)
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
