package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
)

func TestTranslateUserPromptSubmit(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "abc123",
		CWD:           "/tmp/project",
		HookEventName: HookUserPromptSubmit,
		Prompt:        "Please continue carefully",
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	event := events[0]
	if event.Harness != Harness || event.TranscriptID != "abc123" || event.Kind != core.KindText {
		t.Fatalf("event = %#v", event)
	}
	payload := decodePayload[core.TextPayload](t, event.Payload)
	if payload.Actor != core.ActorUser || payload.Content != "Please continue carefully" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestTranslateStopEmitsAssistantThenHarnessStop(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:            "abc123",
		CWD:                  "/tmp/project",
		HookEventName:        HookStop,
		LastAssistantMessage: "I finished the implementation.",
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	first := decodePayload[core.TextPayload](t, events[0].Payload)
	if first.Actor != core.ActorAssistant {
		t.Fatalf("first actor = %s, want assistant", first.Actor)
	}
	second := decodePayload[core.TextPayload](t, events[1].Payload)
	if second.Actor != core.ActorHarness || second.Content != "stop" {
		t.Fatalf("second payload = %#v, want harness stop", second)
	}
}

func TestTranslatePostToolUseWriteRedactsArgsAndAddsFileChange(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "abc123",
		CWD:           "/tmp/project",
		HookEventName: HookPostToolUse,
		ToolName:      "Write",
		ToolUseID:     "toolu_1",
		ToolInput:     json.RawMessage(`{"file_path":"/tmp/project/internal/main.go","content":"secret file content"}`),
		ToolResponse:  json.RawMessage(`{"filePath":"/tmp/project/internal/main.go","success":true}`),
		DurationMS:    12,
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	if events[0].Kind != core.KindToolCall || events[1].Kind != core.KindFileChange || events[2].Kind != core.KindToolResult {
		t.Fatalf("event kinds = %s, %s, %s", events[0].Kind, events[1].Kind, events[2].Kind)
	}
	call := decodePayload[core.ToolCallPayload](t, events[0].Payload)
	var args string
	if err := json.Unmarshal(call.Args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args != core.RedactedValue {
		t.Fatalf("tool call args = %s, want redacted", call.Args)
	}
	change := decodePayload[core.FileChangePayload](t, events[1].Payload)
	if change.Path != "internal/main.go" || change.Op != core.FileOpCreate {
		t.Fatalf("file change = %#v", change)
	}
	result := decodePayload[core.ToolResultPayload](t, events[2].Payload)
	if strings.Contains(result.OutputSummary, "secret file content") {
		t.Fatalf("summary leaked file content: %s", result.OutputSummary)
	}
	if !strings.Contains(result.OutputSummary, "path=internal/main.go") {
		t.Fatalf("summary = %q, want relative path", result.OutputSummary)
	}
}

func TestTranslatePostToolUseBashGitCommitAddsCommitTriggerEvent(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "abc123",
		CWD:           "/tmp/project",
		HookEventName: HookPostToolUse,
		ToolName:      "Bash",
		ToolUseID:     "toolu_1",
		ToolInput:     json.RawMessage(`{"command":"git commit -m test"}`),
		ToolResponse:  json.RawMessage(`{"stdout":"[main abc1234] test\n 1 file changed","stderr":""}`),
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	var commit string
	for _, event := range events {
		if event.Kind != core.KindFileChange {
			continue
		}
		payload := decodePayload[core.FileChangePayload](t, event.Payload)
		commit = payload.GitCommit
	}
	if commit != "abc1234" {
		t.Fatalf("commit = %q, want abc1234", commit)
	}
}

func TestTranslateSessionStart(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "abc123",
		CWD:           "/tmp/project",
		HookEventName: HookSessionStart,
		Source:        "startup",
		Model:         "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	payload := decodePayload[core.TextPayload](t, events[0].Payload)
	if payload.Actor != core.ActorHarness || !strings.Contains(payload.Content, "session_start source=startup") {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestTranslatePreCompactWithExplicitTrigger(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "abc123",
		CWD:           "/tmp/project",
		HookEventName: HookPreCompact,
		Trigger:       "manual",
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	payload := decodePayload[core.TextPayload](t, events[0].Payload)
	if payload.Actor != core.ActorHarness {
		t.Fatalf("actor = %s, want harness", payload.Actor)
	}
	if payload.Content != "pre_compact cause=manual" {
		t.Fatalf("content = %q, want pre_compact cause=manual", payload.Content)
	}
}

func TestTranslatePreCompactDefaultsTriggerToAuto(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "abc123",
		CWD:           "/tmp/project",
		HookEventName: HookPreCompact,
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	payload := decodePayload[core.TextPayload](t, events[0].Payload)
	if payload.Content != "pre_compact cause=auto" {
		t.Fatalf("content = %q, want pre_compact cause=auto", payload.Content)
	}
}

func TestTranslatePostCompact(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "abc123",
		CWD:           "/tmp/project",
		HookEventName: HookPostCompact,
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	payload := decodePayload[core.TextPayload](t, events[0].Payload)
	if payload.Actor != core.ActorHarness {
		t.Fatalf("actor = %s, want harness", payload.Actor)
	}
	if payload.Content != "post_compact" {
		t.Fatalf("content = %q, want post_compact", payload.Content)
	}
}

func testTranslator() Translator {
	return Translator{Now: func() time.Time {
		return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	}}
}

func decodePayload[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	payload, err := core.DecodePayload[T](raw)
	if err != nil {
		t.Fatalf("DecodePayload returned error: %v", err)
	}
	return payload
}
