package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tmjournal "github.com/thinkwright/threadmark/internal/journal"
)

func TestDisableAndEnableCommandToggleProjectMarker(t *testing.T) {
	workingDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := disableCommand([]string{"--working-dir", workingDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("disable exit = %d stderr=%s", code, stderr.String())
	}
	marker, err := disabledMarkerPath(workingDir)
	if err != nil {
		t.Fatalf("disabledMarkerPath returned error: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("stat disabled marker: %v", err)
	}
	if !projectDisabled(workingDir) {
		t.Fatal("projectDisabled returned false after disable")
	}

	stdout.Reset()
	stderr.Reset()
	code = enableCommand([]string{"--working-dir", workingDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("enable exit = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("disabled marker still exists or unexpected stat error: %v", err)
	}
	if projectDisabled(workingDir) {
		t.Fatal("projectDisabled returned true after enable")
	}

	stdout.Reset()
	stderr.Reset()
	code = enableCommand([]string{"--working-dir", workingDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second enable exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already enabled") {
		t.Fatalf("second enable output = %q, want already enabled", stdout.String())
	}
}

func TestPurgeProjectRequiresConfirmation(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".threadmark")
	workingDir := t.TempDir()
	writeStatusFixture(t, root, workingDir)
	store := tmjournal.NewStore(root)
	projectDir, _, err := store.ProjectDir(workingDir)
	if err != nil {
		t.Fatalf("ProjectDir returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := purgeCommand([]string{"--root", root, "--working-dir", workingDir, "--project"}, strings.NewReader("no\n"), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("purge exit = %d, want 1 stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if _, err := os.Stat(projectDir); err != nil {
		t.Fatalf("project dir removed despite cancelled purge: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = purgeCommand([]string{"--root", root, "--working-dir", workingDir, "--project"}, strings.NewReader("purge\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("confirmed purge exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
		t.Fatalf("project dir still exists or unexpected stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "daemon.log")); err != nil {
		t.Fatalf("project purge removed root log or log missing: %v", err)
	}
}

func TestPurgeAllWithYesRemovesThreadmarkRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".threadmark")
	workingDir := t.TempDir()
	writeStatusFixture(t, root, workingDir)

	var stdout, stderr bytes.Buffer
	code := purgeCommand([]string{"--root", root, "--all", "--yes"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("purge all exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root still exists or unexpected stat error: %v", err)
	}
}

func TestPurgeRequiresExactlyOneScope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := purgeCommand(nil, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("purge exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "choose exactly one") {
		t.Fatalf("stderr = %q, want scope error", stderr.String())
	}
}
