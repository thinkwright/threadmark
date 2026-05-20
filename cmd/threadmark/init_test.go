package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitClaudeCodePrintsSettingsFragment(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := initCommand([]string{"claude-code", "--hook-script", "/tmp/threadmark-hook.sh", "--print"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init exit = %d stderr=%s", code, stderr.String())
	}

	var settings map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v\n%s", err, stdout.String())
	}
	hooks := mapValue(settings, "hooks")
	for _, eventName := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "PreCompact", "PostCompact"} {
		groups := arrayValue(hooks, eventName)
		if len(groups) != 1 {
			t.Fatalf("%s groups = %d, want 1", eventName, len(groups))
		}
	}
}

func TestInitClaudeCodeMergesIdempotently(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), ".claude", "settings.json")
	var stdout, stderr bytes.Buffer
	for i := 0; i < 2; i++ {
		code := initCommand([]string{
			"claude-code",
			"--settings-file", settingsFile,
			"--hook-script", "/tmp/threadmark-hook.sh",
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("init %d exit = %d stderr=%s", i, code, stderr.String())
		}
	}

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v\n%s", err, data)
	}
	hooks := mapValue(settings, "hooks")
	for _, eventName := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "PreCompact", "PostCompact"} {
		groups := arrayValue(hooks, eventName)
		if len(groups) != 1 {
			t.Fatalf("%s groups = %d, want 1 after repeated init", eventName, len(groups))
		}
	}
}

func TestInitCodexPrintsHooksFragment(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := initCommand([]string{"codex", "--hook-script", "/tmp/threadmark-hook.sh", "--print"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init exit = %d stderr=%s", code, stderr.String())
	}

	var settings map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v\n%s", err, stdout.String())
	}
	hooks := mapValue(settings, "hooks")
	for _, eventName := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "PreCompact", "PostCompact"} {
		groups := arrayValue(hooks, eventName)
		if len(groups) != 1 {
			t.Fatalf("%s groups = %d, want 1", eventName, len(groups))
		}
		handler := firstHookHandler(t, groups[0])
		if handler["command"] != "/tmp/threadmark-hook.sh" {
			t.Fatalf("%s command = %v", eventName, handler["command"])
		}
		if _, ok := handler["args"]; ok {
			t.Fatalf("%s handler has Claude-style args: %#v", eventName, handler)
		}
	}
}

func TestInitCodexMergesIdempotently(t *testing.T) {
	hooksFile := filepath.Join(t.TempDir(), ".codex", "hooks.json")
	var stdout, stderr bytes.Buffer
	for i := 0; i < 2; i++ {
		code := initCommand([]string{
			"codex",
			"--hooks-file", hooksFile,
			"--hook-script", "/tmp/threadmark-hook.sh",
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("init %d exit = %d stderr=%s", i, code, stderr.String())
		}
	}

	data, err := os.ReadFile(hooksFile)
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal hooks: %v\n%s", err, data)
	}
	hooks := mapValue(settings, "hooks")
	for _, eventName := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "PreCompact", "PostCompact"} {
		groups := arrayValue(hooks, eventName)
		if len(groups) != 1 {
			t.Fatalf("%s groups = %d, want 1 after repeated init", eventName, len(groups))
		}
	}
	if !bytes.Contains(stderr.Bytes(), []byte("run /hooks in Codex")) {
		t.Fatalf("stderr missing trust review note: %s", stderr.String())
	}
}

func TestInitCodexPreservesOtherHooksInSameMatcherGroup(t *testing.T) {
	hooksFile := filepath.Join(t.TempDir(), ".codex", "hooks.json")
	initial := []byte(`{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {"type": "command", "command": "/tmp/threadmark-hook.sh", "timeout": 5},
          {"type": "command", "command": "/tmp/other-hook.sh", "timeout": 5}
        ]
      }
    ]
  }
}`)
	if err := os.MkdirAll(filepath.Dir(hooksFile), 0o700); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	if err := os.WriteFile(hooksFile, initial, 0o600); err != nil {
		t.Fatalf("write hooks: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := initCommand([]string{
		"codex",
		"--hooks-file", hooksFile,
		"--hook-script", "/tmp/threadmark-hook.sh",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init exit = %d stderr=%s", code, stderr.String())
	}

	data, err := os.ReadFile(hooksFile)
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal hooks: %v\n%s", err, data)
	}
	groups := arrayValue(mapValue(settings, "hooks"), "PostToolUse")

	var otherHookCount, threadmarkHookCount int
	for _, group := range groups {
		groupMap, ok := group.(map[string]any)
		if !ok {
			t.Fatalf("group = %#v, want map", group)
		}
		for _, hook := range arrayFromAny(groupMap["hooks"]) {
			hookMap, ok := hook.(map[string]any)
			if !ok {
				t.Fatalf("hook = %#v, want map", hook)
			}
			switch hookMap["command"] {
			case "/tmp/other-hook.sh":
				otherHookCount++
			case "/tmp/threadmark-hook.sh":
				threadmarkHookCount++
			}
		}
	}
	if otherHookCount != 1 {
		t.Fatalf("otherHookCount = %d, want 1\n%s", otherHookCount, data)
	}
	if threadmarkHookCount != 1 {
		t.Fatalf("threadmarkHookCount = %d, want 1\n%s", threadmarkHookCount, data)
	}
}

func firstHookHandler(t *testing.T, group any) map[string]any {
	t.Helper()
	groupMap, ok := group.(map[string]any)
	if !ok {
		t.Fatalf("group = %#v, want map", group)
	}
	handlers := arrayFromAny(groupMap["hooks"])
	if len(handlers) != 1 {
		t.Fatalf("handlers = %d, want 1", len(handlers))
	}
	handler, ok := handlers[0].(map[string]any)
	if !ok {
		t.Fatalf("handler = %#v, want map", handlers[0])
	}
	return handler
}
