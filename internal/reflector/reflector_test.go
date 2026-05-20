package reflector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
)

func TestExtractPromptMarkdownUsesFencedPrompt(t *testing.T) {
	markdown := "# Header\n\nignore\n\n```\nactual prompt\n```\n"
	if got, want := ExtractPromptMarkdown(markdown), "actual prompt"; got != want {
		t.Fatalf("ExtractPromptMarkdown = %q, want %q", got, want)
	}
}

func TestRedactCommonSecretPatterns(t *testing.T) {
	input := strings.Join([]string{
		"AWS_ACCESS_KEY_ID=AKIA1234567890ABCDEF",
		"GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456",
		"OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456",
	}, "\n")

	redacted := Redact(input)
	for _, leaked := range []string{
		"AKIA1234567890ABCDEF",
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"Bearer abcdefghijklmnopqrstuvwxyz123456",
		"sk-abcdefghijklmnopqrstuvwxyz123456",
	} {
		if strings.Contains(redacted, leaked) {
			t.Fatalf("redacted output leaked %q: %s", leaked, redacted)
		}
	}
	if count := strings.Count(redacted, Redacted); count < 4 {
		t.Fatalf("redacted output has %d redactions, want at least 4: %s", count, redacted)
	}
}

func TestRedactStructuredSecretPatterns(t *testing.T) {
	input := strings.Join([]string{
		`{"api_key":"json-secret-1234567890"}`,
		`token: yaml-secret-1234567890`,
		`//registry.npmjs.org/:_authToken=npm-secret-1234567890`,
		"-----BEGIN PRIVATE KEY-----\nprivate-secret-1234567890\n-----END PRIVATE KEY-----",
	}, "\n")

	redacted := Redact(input)
	for _, leaked := range []string{
		"json-secret-1234567890",
		"yaml-secret-1234567890",
		"npm-secret-1234567890",
		"private-secret-1234567890",
	} {
		if strings.Contains(redacted, leaked) {
			t.Fatalf("redacted output leaked %q: %s", leaked, redacted)
		}
	}
	for _, preserved := range []string{
		`"api_key":"<redacted>"`,
		`token: <redacted>`,
		`//registry.npmjs.org/:_authToken=<redacted>`,
	} {
		if !strings.Contains(redacted, preserved) {
			t.Fatalf("redacted output missing preserved shape %q: %s", preserved, redacted)
		}
	}
}

func TestBuildPromptIncludesRedactedCheckpointContext(t *testing.T) {
	checkpoint := testCheckpoint()
	prompt := BuildPrompt("system prompt", Request{
		Checkpoint: checkpoint,
		Transcript: "user: deploy with PASSWORD=hunter2\nassistant: done",
	})

	assertContains(t, prompt, "threadmark:cacheable-system-prompt sha256:")
	assertContains(t, prompt, "system prompt")
	assertContains(t, prompt, "- trigger: commit:abc123")
	assertContains(t, prompt, "```threadmark-events")
	assertContains(t, prompt, "PASSWORD=<redacted>")
	if strings.Contains(prompt, "hunter2") {
		t.Fatalf("prompt leaked secret: %s", prompt)
	}
}

func TestLoadPromptFileFallsBackToEmbeddedDefault(t *testing.T) {
	prompt, err := LoadPromptFile(DefaultPromptPath())
	if err != nil {
		t.Fatalf("LoadPromptFile returned error: %v", err)
	}
	assertContains(t, prompt, "You are reflecting on a coding session.")
	assertContains(t, prompt, "nothing to hand off")
}

func TestClientReflectUsesConvenienceCommand(t *testing.T) {
	promptPath := filepath.Join(t.TempDir(), "reflector.md")
	if err := os.WriteFile(promptPath, []byte("```\nsystem prompt\n```\n"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	runner := &fakeRunner{output: "> Useful line\n\nBody."}
	client, err := NewClient(Config{
		Command:    "claude",
		Mode:       ModeConvenience,
		PromptPath: promptPath,
		Runner:     runner,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	response, err := client.Reflect(context.Background(), Request{
		Checkpoint: testCheckpoint(),
		Transcript: "event excerpt",
	})
	if err != nil {
		t.Fatalf("Reflect returned error: %v", err)
	}
	if response.CommandMode != ModeConvenience {
		t.Fatalf("mode = %s, want convenience", response.CommandMode)
	}
	if response.PromptHash == "" {
		t.Fatal("prompt hash is empty")
	}
	if got, want := runner.command, "claude"; got != want {
		t.Fatalf("command = %s, want %s", got, want)
	}
	if len(runner.args) != 4 ||
		runner.args[0] != "--settings" ||
		runner.args[1] != disableHooksSettings ||
		runner.args[2] != "-p" {
		t.Fatalf("args = %#v, want [--settings disableAllHooks -p prompt]", runner.args)
	}
}

func TestClientReflectUsesBareCommand(t *testing.T) {
	promptPath := filepath.Join(t.TempDir(), "reflector.md")
	if err := os.WriteFile(promptPath, []byte("system prompt"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	runner := &fakeRunner{output: "> Useful line\n\nBody."}
	client, err := NewClient(Config{
		Mode:       ModeBare,
		PromptPath: promptPath,
		Runner:     runner,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if _, err := client.Reflect(context.Background(), Request{Checkpoint: testCheckpoint(), Transcript: "event"}); err != nil {
		t.Fatalf("Reflect returned error: %v", err)
	}
	if len(runner.args) != 3 || runner.args[0] != "--bare" || runner.args[1] != "-p" {
		t.Fatalf("args = %#v, want [--bare -p prompt]", runner.args)
	}
}

func TestExecRunnerMarksReflectorActive(t *testing.T) {
	t.Setenv(ReflectorActiveEnv, "0")

	output, err := ExecRunner{}.Run(context.Background(), "sh", []string{"-c", "printf %s \"$" + ReflectorActiveEnv + "\""})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got, want := output, "1"; got != want {
		t.Fatalf("%s = %q, want %q", ReflectorActiveEnv, got, want)
	}
}

type fakeRunner struct {
	command string
	args    []string
	output  string
}

func (r *fakeRunner) Run(_ context.Context, command string, args []string) (string, error) {
	r.command = command
	r.args = append([]string(nil), args...)
	return r.output, nil
}

func testCheckpoint() core.CheckpointRequest {
	ts := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	return core.CheckpointRequest{
		Trigger: core.TriggerCandidate{
			Type:   core.TriggerCommit,
			Reason: "commit:abc123",
			At:     ts,
		},
		ThreadID:        "t_test",
		TranscriptID:    "transcript-1",
		Harness:         "codex",
		WorkingDir:      "/tmp/project",
		SourceRange:     core.SourceEventRange{First: "evt_1", Last: "evt_2"},
		SourceStartedAt: ts.Add(-time.Minute),
		SourceEndedAt:   ts,
		CommitRefs:      []string{"abc123"},
	}
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}
