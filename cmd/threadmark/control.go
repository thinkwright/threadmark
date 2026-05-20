package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tmjournal "github.com/thinkwright/threadmark/internal/journal"
)

func disableCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("disable", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workingDir := flags.String("working-dir", ".", "project working directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	path, err := disabledMarkerPath(*workingDir)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark disable: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), tmjournal.DirMode); err != nil {
		fmt.Fprintf(stderr, "threadmark disable: create marker directory: %v\n", err)
		return 1
	}
	if err := os.Chmod(filepath.Dir(path), tmjournal.DirMode); err != nil {
		fmt.Fprintf(stderr, "threadmark disable: set marker directory permissions: %v\n", err)
		return 1
	}
	body := fmt.Sprintf("disabled_at: %s\n", time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(path, []byte(body), tmjournal.FileMode); err != nil {
		fmt.Fprintf(stderr, "threadmark disable: write marker: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Threadmark disabled for project: %s\n", filepath.Dir(filepath.Dir(path)))
	fmt.Fprintf(stdout, "marker: %s\n", path)
	return 0
}

func enableCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("enable", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workingDir := flags.String("working-dir", ".", "project working directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	path, err := disabledMarkerPath(*workingDir)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark enable: %v\n", err)
		return 1
	}
	if err := os.Remove(path); err == nil {
		fmt.Fprintf(stdout, "Threadmark enabled for project: %s\n", filepath.Dir(filepath.Dir(path)))
		return 0
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "threadmark enable: remove marker: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Threadmark already enabled for project: %s\n", filepath.Dir(filepath.Dir(path)))
	return 0
}

func purgeCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("purge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workingDir := flags.String("working-dir", ".", "project working directory")
	rootFlag := flags.String("root", "", "threadmark root directory (default ~/.threadmark)")
	project := flags.Bool("project", false, "purge Threadmark storage for the current project")
	all := flags.Bool("all", false, "purge all Threadmark storage under the root")
	yes := flags.Bool("yes", false, "skip confirmation prompt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *project == *all {
		fmt.Fprintln(stderr, "threadmark purge: choose exactly one of --project or --all")
		return 2
	}

	root, err := resolveRoot(*rootFlag)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark purge: %v\n", err)
		return 1
	}
	root, err = safeAbs(root)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark purge: %v\n", err)
		return 1
	}

	var target, description string
	if *project {
		store := tmjournal.NewStore(root)
		projectDir, resolvedWorkingDir, err := store.ProjectDir(*workingDir)
		if err != nil {
			fmt.Fprintf(stderr, "threadmark purge: %v\n", err)
			return 1
		}
		target, err = safeAbs(projectDir)
		if err != nil {
			fmt.Fprintf(stderr, "threadmark purge: %v\n", err)
			return 1
		}
		if err := ensureProjectPurgeTarget(root, target); err != nil {
			fmt.Fprintf(stderr, "threadmark purge: %v\n", err)
			return 1
		}
		description = fmt.Sprintf("project storage for %s", resolvedWorkingDir)
	} else {
		target = root
		if err := ensureRootPurgeTarget(target); err != nil {
			fmt.Fprintf(stderr, "threadmark purge: %v\n", err)
			return 1
		}
		description = fmt.Sprintf("all Threadmark storage under %s", target)
	}

	if !*yes && !confirmPurge(stdin, stderr, description, target) {
		fmt.Fprintln(stdout, "Threadmark purge cancelled.")
		return 1
	}

	if err := os.RemoveAll(target); err != nil {
		fmt.Fprintf(stderr, "threadmark purge: remove %s: %v\n", target, err)
		return 1
	}
	fmt.Fprintf(stdout, "Threadmark purged %s.\n", description)
	fmt.Fprintf(stdout, "removed: %s\n", target)
	return 0
}

func disabledMarkerPath(workingDir string) (string, error) {
	if strings.TrimSpace(workingDir) == "" {
		return "", errors.New("working directory is required")
	}
	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return filepath.Join(abs, ".threadmark", "disabled"), nil
}

func safeAbs(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	return filepath.Clean(abs), nil
}

func ensureProjectPurgeTarget(root, target string) error {
	projectsDir := filepath.Join(root, "projects")
	rel, err := filepath.Rel(projectsDir, target)
	if err != nil {
		return fmt.Errorf("validate project purge target: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to purge project target outside %s: %s", projectsDir, target)
	}
	if strings.Contains(rel, string(filepath.Separator)) {
		return fmt.Errorf("refusing to purge nested project target: %s", target)
	}
	return nil
}

func ensureRootPurgeTarget(root string) error {
	if root == string(filepath.Separator) {
		return errors.New("refusing to purge filesystem root")
	}
	home, err := os.UserHomeDir()
	if err == nil && root == filepath.Clean(home) {
		return errors.New("refusing to purge home directory")
	}
	base := filepath.Base(root)
	if base != ".threadmark" && base != "threadmark" {
		return fmt.Errorf("refusing to purge root %s; expected a threadmark root directory", root)
	}
	return nil
}

func confirmPurge(stdin io.Reader, stderr io.Writer, description, target string) bool {
	fmt.Fprintf(stderr, "This will permanently remove %s.\n", description)
	fmt.Fprintf(stderr, "Target: %s\n", target)
	fmt.Fprint(stderr, `Type "purge" to continue: `)
	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		return false
	}
	return strings.TrimSpace(scanner.Text()) == "purge"
}
