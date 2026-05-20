package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
	tmjournal "github.com/thinkwright/threadmark/internal/journal"
)

func TestStatusCommandPrintsProjectStateAndRecentLogs(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".threadmark")
	workingDir := t.TempDir()
	writeStatusFixture(t, root, workingDir)

	var stdout, stderr bytes.Buffer
	code := statusCommand([]string{
		"--root", root,
		"--working-dir", workingDir,
		"--last", "2",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status exit = %d stderr=%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"daemon: offline",
		"project:",
		"id: ",
		"state: ",
		"journal: ",
		"entries=1",
		"current_thread: t_status",
		"last_harness: codex",
		"pending_trigger: stop",
		"recent_log:",
		"event.received",
		"trigger.fired",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("status output missing %q:\n%s", want, output)
		}
	}
}

func TestStatusCommandPrintsJSON(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".threadmark")
	workingDir := t.TempDir()
	writeStatusFixture(t, root, workingDir)

	var stdout, stderr bytes.Buffer
	code := statusCommand([]string{
		"--root", root,
		"--working-dir", workingDir,
		"--last", "1",
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status exit = %d stderr=%s", code, stderr.String())
	}

	var report statusReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal status JSON: %v\n%s", err, stdout.String())
	}
	if report.Root != root {
		t.Fatalf("root = %q, want %q", report.Root, root)
	}
	if report.Project.ID == "" || report.Project.WorkingDir == "" {
		t.Fatalf("project fields missing: %#v", report.Project)
	}
	if !report.Project.StateExists {
		t.Fatal("expected state_exists")
	}
	if !report.Project.JournalExists || report.Project.JournalEntries != 1 {
		t.Fatalf("journal exists=%v entries=%d, want true/1", report.Project.JournalExists, report.Project.JournalEntries)
	}
	if report.Project.PendingTrigger == nil || report.Project.PendingTrigger.Reason != "stop" {
		t.Fatalf("pending trigger = %#v, want stop", report.Project.PendingTrigger)
	}
	if len(report.RecentLogs) != 1 || report.RecentLogs[0].Kind != "trigger.fired" {
		t.Fatalf("recent logs = %#v, want last trigger.fired", report.RecentLogs)
	}
}

func writeStatusFixture(t *testing.T, root, workingDir string) {
	t.Helper()

	store := tmjournal.NewStore(root)
	if _, _, err := store.EnsureProject(workingDir); err != nil {
		t.Fatalf("EnsureProject returned error: %v", err)
	}
	writeTestJournalEntry(t, root, workingDir)

	now := time.Date(2026, 5, 18, 18, 0, 0, 0, time.UTC)
	statePath, resolvedWorkingDir, err := store.StatePath(workingDir)
	if err != nil {
		t.Fatalf("StatePath returned error: %v", err)
	}
	state := core.ProjectState{
		SchemaVersion:        core.CurrentSchemaVersion,
		ProjectHash:          "fixture",
		WorkingDir:           resolvedWorkingDir,
		CurrentThreadID:      "t_status",
		TranscriptToThread:   map[string]string{"transcript-1": "t_status"},
		LastTranscriptID:     "transcript-1",
		LastHarness:          "codex",
		LastActivityTS:       now,
		LastCheckpointTS:     now.Add(-time.Minute),
		LastProcessedEventID: "evt_stop",
		CurrentRangeStartID:  "evt_user",
		TurnsSinceCheckpoint: 1,
		SubstantiveEvents:    1,
		Debounce: core.DebounceState{
			PendingTrigger: &core.TriggerCandidate{
				Type:     core.TriggerStop,
				Reason:   "stop",
				Priority: 85,
				EventID:  "evt_stop",
				At:       now,
			},
		},
	}
	data, err := json.MarshalIndent(state.Normalize(), "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(statePath, append(data, '\n'), tmjournal.FileMode); err != nil {
		t.Fatalf("write state: %v", err)
	}

	logPath := filepath.Join(root, "daemon.log")
	log := strings.Join([]string{
		`{"ts":"2026-05-18T18:00:00Z","kind":"event.received","project_id":"fixture","thread_id":"t_status","harness":"codex","event_kind":"text"}`,
		`{"ts":"2026-05-18T18:00:01Z","kind":"trigger.fired","project_id":"fixture","thread_id":"t_status","trigger":"stop","reason":"stop"}`,
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(log), tmjournal.FileMode); err != nil {
		t.Fatalf("write daemon log: %v", err)
	}
}
