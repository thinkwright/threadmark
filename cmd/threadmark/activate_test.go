package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActivateCommandInstallsDefaultProjectHooksQuietly(t *testing.T) {
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	daemonStarted := stubActivateStartDaemon(t)

	var stdout, stderr bytes.Buffer
	code := activateCommand([]string{"--quiet"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("activate exit = %d stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	assertFileContains(t, filepath.Join(projectDir, ".claude", "settings.json"), "\"command\": \"threadmark\"")
	assertFileContains(t, filepath.Join(projectDir, ".claude", "settings.json"), "\"claude-code\"")
	assertFileContains(t, filepath.Join(projectDir, ".codex", "hooks.json"), "threadmark hook codex")
	if !*daemonStarted {
		t.Fatalf("activate did not start daemon")
	}
}

func TestActivateCommandSupportsHarnessSelection(t *testing.T) {
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	stubActivateStartDaemon(t)

	var stdout, stderr bytes.Buffer
	code := activateCommand([]string{"--harness", "claudecode", "--quiet"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("activate exit = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".claude", "settings.json")); err != nil {
		t.Fatalf("claude settings not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("codex hooks should not exist, stat err=%v", err)
	}
}

func TestActivateCommandReportsDaemonReady(t *testing.T) {
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	stubActivateStartDaemon(t)

	var stdout, stderr bytes.Buffer
	code := activateCommand(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("activate exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "threadmark: daemon ready") {
		t.Fatalf("stdout missing daemon ready message: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "threadmark: project activation complete") {
		t.Fatalf("stdout missing activation complete message: %s", stdout.String())
	}
}

func TestActivateCommandRejectsUnknownHarness(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := activateCommand([]string{"--harness", "unknown"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("activate exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown harness") {
		t.Fatalf("stderr missing unknown harness: %s", stderr.String())
	}
}

func stubActivateStartDaemon(t *testing.T) *bool {
	t.Helper()
	started := false
	old := activateStartDaemon
	activateStartDaemon = func(quiet bool, stdout, stderr io.Writer) int {
		started = true
		if !quiet {
			fmt.Fprintln(stdout, "threadmark: daemon ready")
		}
		return 0
	}
	t.Cleanup(func() {
		activateStartDaemon = old
	})
	return &started
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s missing %q:\n%s", path, want, string(data))
	}
}
