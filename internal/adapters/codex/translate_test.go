package codex

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
)

func TestTranslateUserPromptSubmit(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "codex-session",
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
	if event.Harness != Harness || event.TranscriptID != "codex-session" || event.Kind != core.KindText {
		t.Fatalf("event = %#v", event)
	}
	payload := decodePayload[core.TextPayload](t, event.Payload)
	if payload.Actor != core.ActorUser || payload.Content != "Please continue carefully" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestTranslateSessionStart(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "codex-session",
		CWD:           "/tmp/project",
		HookEventName: HookSessionStart,
		Source:        "startup",
		Model:         "gpt-5.4-mini",
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	payload := decodePayload[core.TextPayload](t, events[0].Payload)
	if payload.Actor != core.ActorHarness || !strings.Contains(payload.Content, "session_start source=startup") {
		t.Fatalf("payload = %#v", payload)
	}
	if !strings.Contains(payload.Content, "model=gpt-5.4-mini") {
		t.Fatalf("payload = %#v, want model", payload)
	}
}

func TestTranslateStopEmitsAssistantThenHarnessStop(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:            "codex-session",
		CWD:                  "/tmp/project",
		HookEventName:        HookStop,
		LastAssistantMessage: "I finished.",
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	first := decodePayload[core.TextPayload](t, events[0].Payload)
	if first.Actor != core.ActorAssistant || first.Content != "I finished." {
		t.Fatalf("first payload = %#v", first)
	}
	second := decodePayload[core.TextPayload](t, events[1].Payload)
	if second.Actor != core.ActorHarness || second.Content != "stop" {
		t.Fatalf("second payload = %#v", second)
	}
}

func TestTranslatePreCompactManualAndAuto(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger string
		want    string
	}{
		{name: "manual", trigger: "manual", want: "pre_compact cause=manual"},
		{name: "auto fallback", trigger: "background", want: "pre_compact cause=auto"},
		{name: "empty", trigger: "", want: "pre_compact cause=auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, err := testTranslator().Translate(HookPayload{
				SessionID:     "codex-session",
				CWD:           "/tmp/project",
				HookEventName: HookPreCompact,
				Trigger:       tc.trigger,
			})
			if err != nil {
				t.Fatalf("Translate returned error: %v", err)
			}
			payload := decodePayload[core.TextPayload](t, events[0].Payload)
			if payload.Actor != core.ActorHarness || payload.Content != tc.want {
				t.Fatalf("payload = %#v, want %q", payload, tc.want)
			}
		})
	}
}

func TestTranslatePostCompact(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "codex-session",
		CWD:           "/tmp/project",
		HookEventName: HookPostCompact,
		Trigger:       "manual",
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	payload := decodePayload[core.TextPayload](t, events[0].Payload)
	if payload.Actor != core.ActorHarness || payload.Content != "post_compact" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestTranslatePostToolUseBashGitCommitAddsCommitTriggerEvent(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "codex-session",
		CWD:           "/tmp/project",
		HookEventName: HookPostToolUse,
		ToolName:      "Bash",
		ToolUseID:     "call_1",
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

func TestTranslatePostToolUseApplyPatchAddsFileChangesAndRedactsArgs(t *testing.T) {
	events, err := testTranslator().Translate(HookPayload{
		SessionID:     "codex-session",
		CWD:           "/tmp/project",
		HookEventName: HookPostToolUse,
		ToolName:      "apply_patch",
		ToolUseID:     "call_1",
		ToolInput:     json.RawMessage(`{"command":"*** Begin Patch\n*** Add File: internal/new.go\n+package internal\n*** Update File: /tmp/project/internal/existing.go\n@@\n-old\n+new\n*** Delete File: old.txt\n*** End Patch\n"}`),
		ToolResponse:  json.RawMessage(`{"status":"ok"}`),
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("len(events) = %d, want 5", len(events))
	}
	call := decodePayload[core.ToolCallPayload](t, events[0].Payload)
	var args string
	if err := json.Unmarshal(call.Args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args != core.RedactedValue {
		t.Fatalf("tool call args = %s, want redacted", call.Args)
	}
	changes := map[string]core.FileOp{}
	for _, event := range events {
		if event.Kind != core.KindFileChange {
			continue
		}
		change := decodePayload[core.FileChangePayload](t, event.Payload)
		changes[change.Path] = change.Op
	}
	if changes["internal/new.go"] != core.FileOpCreate {
		t.Fatalf("new.go op = %s", changes["internal/new.go"])
	}
	if changes["internal/existing.go"] != core.FileOpModify {
		t.Fatalf("existing.go op = %s", changes["internal/existing.go"])
	}
	if changes["old.txt"] != core.FileOpDelete {
		t.Fatalf("old.txt op = %s", changes["old.txt"])
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
