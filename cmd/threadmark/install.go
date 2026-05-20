package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type installOptions struct {
	BinDir         string
	ThreadmarkPath string
	DaemonPath     string
	DryRun         bool
}

type installReport struct {
	BinDir       string
	Threadmark   installFileReport
	Daemon       installFileReport
	BinDirOnPath bool
	DryRun       bool
}

type installFileReport struct {
	Source      string
	Destination string
	Action      string
}

func installCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	binDir := flags.String("bin-dir", "", "destination directory for binaries (default ~/.local/bin)")
	threadmarkPath := flags.String("threadmark", "", "threadmark binary to install (default current executable)")
	daemonPath := flags.String("threadmarkd", "", "threadmarkd binary to install (default sibling binary or PATH)")
	dryRun := flags.Bool("dry-run", false, "print what would be installed without writing files")
	quiet := flags.Bool("quiet", false, "suppress non-error output")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "threadmark install: unexpected argument %q\n", flags.Arg(0))
		return 2
	}

	report, err := installBinaries(installOptions{
		BinDir:         *binDir,
		ThreadmarkPath: *threadmarkPath,
		DaemonPath:     *daemonPath,
		DryRun:         *dryRun,
	})
	if err != nil {
		fmt.Fprintf(stderr, "threadmark install: %v\n", err)
		return 1
	}

	if !*quiet {
		printInstallReport(stdout, stderr, report)
	}
	return 0
}

func installBinaries(opts installOptions) (installReport, error) {
	binDir, err := resolveInstallBinDir(opts.BinDir)
	if err != nil {
		return installReport{}, err
	}
	threadmarkSrc, err := resolveInstallThreadmarkSource(opts.ThreadmarkPath)
	if err != nil {
		return installReport{}, err
	}
	daemonSrc, err := resolveInstallDaemonSource(opts.DaemonPath, threadmarkSrc)
	if err != nil {
		return installReport{}, err
	}

	if !opts.DryRun {
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return installReport{}, fmt.Errorf("create install bin dir: %w", err)
		}
	}

	threadmarkDst := filepath.Join(binDir, installBinaryName("threadmark"))
	daemonDst := filepath.Join(binDir, installBinaryName("threadmarkd"))
	threadmarkReport, err := installExecutable(threadmarkSrc, threadmarkDst, opts.DryRun)
	if err != nil {
		return installReport{}, err
	}
	daemonReport, err := installExecutable(daemonSrc, daemonDst, opts.DryRun)
	if err != nil {
		return installReport{}, err
	}

	return installReport{
		BinDir:       binDir,
		Threadmark:   threadmarkReport,
		Daemon:       daemonReport,
		BinDirOnPath: pathContainsDir(os.Getenv("PATH"), binDir),
		DryRun:       opts.DryRun,
	}, nil
}

func resolveInstallBinDir(value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return filepath.Abs(value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func resolveInstallThreadmarkSource(value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return requireInstallExecutable(value, "threadmark")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find current executable: %w", err)
	}
	return requireInstallExecutable(exe, "threadmark")
}

func resolveInstallDaemonSource(value, threadmarkSrc string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return requireInstallExecutable(value, "threadmarkd")
	}
	sibling := filepath.Join(filepath.Dir(threadmarkSrc), installBinaryName("threadmarkd"))
	if path, err := requireInstallExecutable(sibling, "threadmarkd"); err == nil {
		return path, nil
	}
	if path, err := exec.LookPath(installBinaryName("threadmarkd")); err == nil {
		return requireInstallExecutable(path, "threadmarkd")
	}
	return "", fmt.Errorf("find threadmarkd: expected executable sibling %s or threadmarkd on PATH", sibling)
}

func requireInstallExecutable(path, name string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s path: %w", name, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %s %s: %w", name, abs, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s %s is not a regular file", name, abs)
	}
	if info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s %s is not executable", name, abs)
	}
	return abs, nil
}

func installExecutable(src, dst string, dryRun bool) (installFileReport, error) {
	report := installFileReport{Source: src, Destination: dst}
	if sameFile(src, dst) {
		report.Action = "already installed"
		return report, nil
	}
	if sameExecutableContents(src, dst) {
		report.Action = "already current"
		if !dryRun {
			if err := os.Chmod(dst, 0o755); err != nil {
				return report, fmt.Errorf("set executable permissions on %s: %w", dst, err)
			}
		}
		return report, nil
	}
	if info, err := os.Stat(dst); err == nil {
		if !info.Mode().IsRegular() {
			return report, fmt.Errorf("install destination %s is not a regular file", dst)
		}
		report.Action = "updated"
	} else if errors.Is(err, os.ErrNotExist) {
		report.Action = "installed"
	} else {
		return report, fmt.Errorf("stat install destination %s: %w", dst, err)
	}
	if dryRun {
		switch report.Action {
		case "updated":
			report.Action = "would update"
		case "installed":
			report.Action = "would install"
		default:
			report.Action = "would " + report.Action
		}
		return report, nil
	}
	if err := copyExecutableAtomic(src, dst); err != nil {
		return report, err
	}
	return report, nil
}

func sameFile(a, b string) bool {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return os.SameFile(aInfo, bInfo)
}

func sameExecutableContents(src, dst string) bool {
	srcData, srcErr := os.ReadFile(src)
	dstData, dstErr := os.ReadFile(dst)
	if srcErr != nil || dstErr != nil {
		return false
	}
	return bytes.Equal(srcData, dstData)
}

func copyExecutableAtomic(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create install destination dir: %w", err)
	}
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open install source %s: %w", src, err)
	}
	defer srcFile.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp.")
	if err != nil {
		return fmt.Errorf("create install temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := io.Copy(tmp, srcFile); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set install temp permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close install temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("replace %s: %w", dst, err)
	}
	return nil
}

func pathContainsDir(pathValue, dir string) bool {
	if strings.TrimSpace(pathValue) == "" {
		return false
	}
	target, err := filepath.Abs(dir)
	if err != nil {
		target = filepath.Clean(dir)
	}
	for _, entry := range filepath.SplitList(pathValue) {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		entryAbs, err := filepath.Abs(entry)
		if err != nil {
			entryAbs = filepath.Clean(entry)
		}
		if entryAbs == target {
			return true
		}
	}
	return false
}

func printInstallReport(stdout, stderr io.Writer, report installReport) {
	if report.DryRun {
		fmt.Fprintln(stdout, "threadmark install dry run")
	}
	fmt.Fprintf(stdout, "threadmark: %s %s -> %s\n", report.Threadmark.Action, report.Threadmark.Source, report.Threadmark.Destination)
	fmt.Fprintf(stdout, "threadmarkd: %s %s -> %s\n", report.Daemon.Action, report.Daemon.Source, report.Daemon.Destination)
	if !report.BinDirOnPath {
		fmt.Fprintf(stderr, "threadmark install: %s is not on PATH; add it to the shell environment that launches your agents\n", report.BinDir)
	}
}

func installBinaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
