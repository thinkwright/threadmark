package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestDaemonPIDFileRoundTrip(t *testing.T) {
	root := t.TempDir()

	if err := writeDaemonPID(root, 12345); err != nil {
		t.Fatalf("writeDaemonPID returned error: %v", err)
	}
	pid, err := readDaemonPID(root)
	if err != nil {
		t.Fatalf("readDaemonPID returned error: %v", err)
	}
	if pid != 12345 {
		t.Fatalf("pid = %d, want 12345", pid)
	}
	if err := removeDaemonPID(root); err != nil {
		t.Fatalf("removeDaemonPID returned error: %v", err)
	}
	if _, err := readDaemonPID(root); err == nil {
		t.Fatal("readDaemonPID succeeded after remove, want error")
	}
}

func TestDaemonStatusReportsNotRunning(t *testing.T) {
	root := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := daemonCommand([]string{"status", "--root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("daemon status exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "threadmarkd not running") {
		t.Fatalf("stdout = %q, want not running", stdout.String())
	}
}

func TestVerifyDaemonProcessRejectsNonThreadmarkProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process verification is Unix-specific")
	}
	err := verifyDaemonProcess(os.Getpid(), t.TempDir(), filepath.Join(t.TempDir(), "daemon.sock"))
	if err == nil {
		t.Fatal("verifyDaemonProcess returned nil for test process")
	}
	if !strings.Contains(err.Error(), "not threadmarkd") && !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartDaemonWritesPIDFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake daemon is Unix-specific")
	}

	root := t.TempDir()
	socketPath := filepath.Join(root, "daemon.sock")
	fakeDaemon := filepath.Join(t.TempDir(), "fake-threadmarkd.sh")
	if err := os.WriteFile(fakeDaemon, []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
		t.Fatalf("write fake daemon: %v", err)
	}

	if err := startDaemon(root, socketPath, fakeDaemon); err != nil {
		t.Fatalf("startDaemon returned error: %v", err)
	}
	pid, err := readDaemonPID(root)
	if err != nil {
		t.Fatalf("readDaemonPID returned error: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("pid = %d, want positive", pid)
	}
	t.Cleanup(func() {
		process, findErr := os.FindProcess(pid)
		if findErr == nil {
			_ = process.Signal(syscall.SIGTERM)
		}
		_ = removeDaemonPID(root)
	})

	data, err := os.ReadFile(daemonPIDPath(root))
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(pid) {
		t.Fatalf("pid file = %q, want %d", string(data), pid)
	}
}
