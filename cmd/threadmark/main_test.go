package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/thinkwright/threadmark/internal/buildinfo"
)

func TestMainVersionCommand(t *testing.T) {
	stdout, stderr, code := runMainForTest(t, []string{"threadmark", "version"})
	if code != 0 {
		t.Fatalf("version exit = %d stderr=%s", code, stderr)
	}
	if got, want := strings.TrimSpace(stdout), buildinfo.Version; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func runMainForTest(t *testing.T, args []string) (stdout string, stderr string, code int) {
	t.Helper()

	oldArgs := os.Args
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	defer func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	os.Args = args
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter

	main()

	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	if _, err := stdoutBuf.ReadFrom(stdoutReader); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if _, err := stderrBuf.ReadFrom(stderrReader); err != nil {
		t.Fatalf("read stderr: %v", err)
	}

	return stdoutBuf.String(), stderrBuf.String(), 0
}
