package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCommandCopiesThreadmarkAndDaemon(t *testing.T) {
	srcDir := t.TempDir()
	binDir := t.TempDir()
	threadmarkSrc := writeTestExecutable(t, srcDir, "threadmark", "threadmark v1\n")
	daemonSrc := writeTestExecutable(t, srcDir, "threadmarkd", "threadmarkd v1\n")
	t.Setenv("PATH", binDir)

	var stdout, stderr bytes.Buffer
	code := installCommand([]string{
		"--bin-dir", binDir,
		"--threadmark", threadmarkSrc,
		"--threadmarkd", daemonSrc,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install exit = %d stderr=%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	assertFileContents(t, filepath.Join(binDir, installBinaryName("threadmark")), "threadmark v1\n")
	assertFileContents(t, filepath.Join(binDir, installBinaryName("threadmarkd")), "threadmarkd v1\n")
	if !strings.Contains(stdout.String(), "threadmark: installed") {
		t.Fatalf("stdout missing install action: %s", stdout.String())
	}
}

func TestInstallCommandDefaultsDaemonToSibling(t *testing.T) {
	srcDir := t.TempDir()
	binDir := t.TempDir()
	threadmarkSrc := writeTestExecutable(t, srcDir, "threadmark", "threadmark v1\n")
	writeTestExecutable(t, srcDir, "threadmarkd", "threadmarkd sibling\n")
	t.Setenv("PATH", binDir)

	var stdout, stderr bytes.Buffer
	code := installCommand([]string{
		"--bin-dir", binDir,
		"--threadmark", threadmarkSrc,
		"--quiet",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install exit = %d stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	assertFileContents(t, filepath.Join(binDir, installBinaryName("threadmarkd")), "threadmarkd sibling\n")
}

func TestInstallCommandIsIdempotent(t *testing.T) {
	srcDir := t.TempDir()
	binDir := t.TempDir()
	threadmarkSrc := writeTestExecutable(t, srcDir, "threadmark", "threadmark v1\n")
	daemonSrc := writeTestExecutable(t, srcDir, "threadmarkd", "threadmarkd v1\n")
	t.Setenv("PATH", binDir)

	for i := 0; i < 2; i++ {
		var stdout, stderr bytes.Buffer
		code := installCommand([]string{
			"--bin-dir", binDir,
			"--threadmark", threadmarkSrc,
			"--threadmarkd", daemonSrc,
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("install %d exit = %d stderr=%s", i, code, stderr.String())
		}
		if i == 1 && !strings.Contains(stdout.String(), "already current") {
			t.Fatalf("second stdout missing already current: %s", stdout.String())
		}
	}
}

func TestInstallCommandDryRunDoesNotWrite(t *testing.T) {
	srcDir := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "bin")
	threadmarkSrc := writeTestExecutable(t, srcDir, "threadmark", "threadmark v1\n")
	daemonSrc := writeTestExecutable(t, srcDir, "threadmarkd", "threadmarkd v1\n")
	t.Setenv("PATH", binDir)

	var stdout, stderr bytes.Buffer
	code := installCommand([]string{
		"--bin-dir", binDir,
		"--threadmark", threadmarkSrc,
		"--threadmarkd", daemonSrc,
		"--dry-run",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install exit = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(binDir, installBinaryName("threadmark"))); !os.IsNotExist(err) {
		t.Fatalf("dry run destination exists or unexpected stat err: %v", err)
	}
	if !strings.Contains(stdout.String(), "would install") {
		t.Fatalf("stdout missing dry-run action: %s", stdout.String())
	}
}

func TestInstallCommandWarnsWhenBinDirNotOnPath(t *testing.T) {
	srcDir := t.TempDir()
	binDir := t.TempDir()
	threadmarkSrc := writeTestExecutable(t, srcDir, "threadmark", "threadmark v1\n")
	daemonSrc := writeTestExecutable(t, srcDir, "threadmarkd", "threadmarkd v1\n")
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := installCommand([]string{
		"--bin-dir", binDir,
		"--threadmark", threadmarkSrc,
		"--threadmarkd", daemonSrc,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "is not on PATH") {
		t.Fatalf("stderr missing PATH warning: %s", stderr.String())
	}
}

func writeTestExecutable(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, installBinaryName(name))
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
	return path
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}
