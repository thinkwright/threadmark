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

func TestDoctorCommandReportsHealthyProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".threadmark")
	workingDir := t.TempDir()
	socketPath := filepath.Join(root, "daemon.sock")
	startTestIPCServer(t, socketPath, 0)
	if err := writeDaemonPID(root, os.Getpid()); err != nil {
		t.Fatalf("writeDaemonPID returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "daemon.log"), []byte("{}\n"), tmjournal.FileMode); err != nil {
		t.Fatalf("write daemon log: %v", err)
	}
	writeDoctorProjectFixture(t, root, workingDir)
	writeDoctorHookFile(t, claudeCodeSettingsFragment("/tmp/threadmark-hook.sh", nil), filepath.Join(workingDir, ".claude", "settings.json"))
	writeDoctorHookFile(t, codexHooksFragment("/tmp/threadmark-hook.sh"), filepath.Join(workingDir, ".codex", "hooks.json"))
	promptPath := filepath.Join(t.TempDir(), "reflector.md")
	if err := os.WriteFile(promptPath, []byte("```\nreflect carefully\n```\n"), tmjournal.FileMode); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := doctorCommand([]string{
		"--root", root,
		"--socket", socketPath,
		"--working-dir", workingDir,
		"--reflector-command", exe,
		"--reflector-prompt", promptPath,
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal doctor JSON: %v\n%s", err, stdout.String())
	}
	if report.Summary.Fail != 0 || report.Summary.Warn != 0 {
		t.Fatalf("summary = %#v, want no warn/fail; checks=%#v", report.Summary, report.Checks)
	}
	if !doctorHasCheck(report, "daemon", doctorOK) {
		t.Fatalf("daemon ok check missing: %#v", report.Checks)
	}
	if !doctorHasCheck(report, "claude_hooks", doctorOK) || !doctorHasCheck(report, "codex_hooks", doctorOK) {
		t.Fatalf("hook ok checks missing: %#v", report.Checks)
	}
}

func TestDoctorCommandWarnsWhenDaemonStoppedAndFailsForReflector(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".threadmark")
	workingDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := doctorCommand([]string{
		"--root", root,
		"--working-dir", workingDir,
		"--reflector-command", "definitely-not-threadmark-reflector",
		"--reflector-prompt", filepath.Join(t.TempDir(), "missing-reflector.md"),
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("doctor exit = %d, want 1 stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"[WARN] daemon:",
		"[FAIL] reflector_command:",
		"[FAIL] reflector_prompt:",
		"[WARN] claude_hooks:",
		"[WARN] codex_hooks:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
}

func TestDoctorCommandWarnsForFreshStoppedDaemon(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".threadmark")
	workingDir := t.TempDir()
	promptPath := filepath.Join(t.TempDir(), "reflector.md")
	if err := os.WriteFile(promptPath, []byte("```\nreflect carefully\n```\n"), tmjournal.FileMode); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := doctorCommand([]string{
		"--root", root,
		"--working-dir", workingDir,
		"--reflector-command", exe,
		"--reflector-prompt", promptPath,
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor exit = %d, want 0 stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal doctor JSON: %v\n%s", err, stdout.String())
	}
	if report.Summary.Fail != 0 {
		t.Fatalf("summary = %#v, want no fails; checks=%#v", report.Summary, report.Checks)
	}
	if !doctorHasCheck(report, "daemon", doctorWarn) {
		t.Fatalf("daemon warn check missing: %#v", report.Checks)
	}
}

func writeDoctorProjectFixture(t *testing.T, root, workingDir string) {
	t.Helper()
	store := tmjournal.NewStore(root)
	if _, _, err := store.EnsureProject(workingDir); err != nil {
		t.Fatalf("EnsureProject returned error: %v", err)
	}
	writeTestJournalEntry(t, root, workingDir)

	now := time.Date(2026, 5, 18, 19, 0, 0, 0, time.UTC)
	statePath, resolvedWorkingDir, err := store.StatePath(workingDir)
	if err != nil {
		t.Fatalf("StatePath returned error: %v", err)
	}
	state := core.ProjectState{
		SchemaVersion:        core.CurrentSchemaVersion,
		ProjectHash:          "fixture",
		WorkingDir:           resolvedWorkingDir,
		CurrentThreadID:      "t_doctor",
		TranscriptToThread:   map[string]string{"transcript-1": "t_doctor"},
		LastTranscriptID:     "transcript-1",
		LastHarness:          "codex",
		LastActivityTS:       now,
		LastCheckpointTS:     now.Add(-time.Minute),
		LastProcessedEventID: "evt_2",
	}
	data, err := json.MarshalIndent(state.Normalize(), "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(statePath, append(data, '\n'), tmjournal.FileMode); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func writeDoctorHookFile(t *testing.T, value map[string]any, path string) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal hook file: %v", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), tmjournal.DirMode); err != nil {
		t.Fatalf("mkdir hook file dir: %v", err)
	}
	if err := os.WriteFile(path, data, tmjournal.FileMode); err != nil {
		t.Fatalf("write hook file: %v", err)
	}
	return path
}

func doctorHasCheck(report doctorReport, name string, status doctorSeverity) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}
