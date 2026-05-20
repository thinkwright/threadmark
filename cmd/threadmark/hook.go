package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thinkwright/threadmark/internal/adapters/claudecode"
	"github.com/thinkwright/threadmark/internal/adapters/codex"
	"github.com/thinkwright/threadmark/internal/core"
	"github.com/thinkwright/threadmark/internal/ipc"
	tmjournal "github.com/thinkwright/threadmark/internal/journal"
	"github.com/thinkwright/threadmark/internal/reflector"
)

const sessionContextPreamble = "Threadmark startup packet: current workspace facts plus selected *perspectival journal entries* from previous sessions on this project. Journal entries reflect what past Threadmark reflectors thought mattered at the time, not factual ground truth. They may be incomplete, biased, stale, or wrong. Use the packet as orientation, not as authority; verify against git history, current code, tests, and durable project docs when correctness matters."

const (
	projectCardEnv      = "THREADMARK_PROJECT_CARD"
	projectCardMaxRunes = 4096
)

var lowSignalJournalMarkers = []string{
	"can't honestly reconstruct",
	"continuity signal here is zero",
	"empty event range",
	"empty session",
	"hook-validation",
	"no actual task",
	"no salience",
	"no transcript excerpt",
	"nothing happened",
	"nothing to hand off",
	"plumbing artifact",
	"skip this sub-chain",
}

func hook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: threadmark hook <claude-code|codex> [options]")
		return 2
	}
	switch args[0] {
	case "claude-code", "claudecode":
		return hookClaudeCode(args[1:], stdin, stdout, stderr)
	case "codex":
		return hookCodex(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "threadmark: unknown hook adapter %q\n", args[0])
		return 2
	}
}

func hookClaudeCode(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return hookTranslated("hook claude-code", args, stdin, stdout, stderr, func(data []byte) (translatedHook, error) {
		payload, err := claudecode.DecodeHookPayload(data)
		if err != nil {
			return translatedHook{}, err
		}
		events, err := claudecode.Translate(payload)
		if err != nil {
			return translatedHook{}, err
		}
		return translatedHook{
			cwd:          payload.CWD,
			sessionStart: payload.HookEventName == claudecode.HookSessionStart,
			events:       events,
		}, nil
	})
}

func hookCodex(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return hookTranslated("hook codex", args, stdin, stdout, stderr, func(data []byte) (translatedHook, error) {
		payload, err := codex.DecodeHookPayload(data)
		if err != nil {
			return translatedHook{}, err
		}
		events, err := codex.Translate(payload)
		if err != nil {
			return translatedHook{}, err
		}
		return translatedHook{
			cwd:          payload.CWD,
			sessionStart: payload.HookEventName == codex.HookSessionStart,
			events:       events,
		}, nil
	})
}

type translatedHook struct {
	cwd          string
	sessionStart bool
	events       []core.Event
}

func hookTranslated(commandName string, args []string, stdin io.Reader, stdout, stderr io.Writer, translate func([]byte) (translatedHook, error)) int {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	rootFlag := flags.String("root", "", "threadmark root directory (default ~/.threadmark)")
	socketFlag := flags.String("socket", "", "unix socket path (default ~/.threadmark/daemon.sock)")
	daemonFlag := flags.String("daemon-command", "", "threadmarkd command path (default sibling binary or PATH)")
	noSpawn := flags.Bool("no-spawn", false, "do not auto-spawn threadmarkd")
	strict := flags.Bool("strict", false, "return non-zero on bridge failure")
	sessionEntries := flags.Int("session-context-entries", 3, "journal entries to inject on SessionStart")
	sessionScan := flags.Int("session-context-scan", 12, "recent journal entries to scan for SessionStart context")
	timeout := flags.Duration("timeout", 1500*time.Millisecond, "hook bridge timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if os.Getenv(reflector.ReflectorActiveEnv) == "1" {
		return 0
	}

	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return hookFailure(root, *strict, stderr, err)
	}
	socketPath, err := resolveSocket(root, *socketFlag)
	if err != nil {
		return hookFailure(root, *strict, stderr, err)
	}

	data, err := io.ReadAll(stdin)
	if err != nil {
		return hookFailure(root, *strict, stderr, fmt.Errorf("read hook stdin: %w", err))
	}
	translated, err := translate(data)
	if err != nil {
		return hookFailure(root, *strict, stderr, err)
	}
	if projectDisabled(translated.cwd) {
		return 0
	}

	var contextText string
	var contextErr error
	if translated.sessionStart && *sessionEntries > 0 {
		contextText, contextErr = sessionContext(root, translated.cwd, *sessionEntries, *sessionScan)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var bridgeErr error
	if !*noSpawn {
		if err := ensureDaemon(ctx, root, socketPath, *daemonFlag); err != nil {
			bridgeErr = err
		}
	}
	if bridgeErr == nil {
		if err := sendEvents(ctx, socketPath, translated.events); err != nil {
			bridgeErr = err
		}
	}

	if translated.sessionStart && contextText != "" {
		if err := writeSessionStartContext(stdout, "SessionStart", contextText); err != nil {
			return hookFailure(root, *strict, stderr, err)
		}
	}
	if contextErr != nil {
		if bridgeErr != nil {
			return hookFailure(root, *strict && contextText == "", stderr, fmt.Errorf("%v; session context: %w", bridgeErr, contextErr))
		}
		return hookFailure(root, *strict, stderr, contextErr)
	}
	if bridgeErr != nil {
		return hookFailure(root, *strict && contextText == "", stderr, bridgeErr)
	}

	return 0
}

func resolveRoot(value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return value, nil
	}
	return tmjournal.DefaultRoot()
}

func resolveSocket(root, value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return value, nil
	}
	if strings.TrimSpace(root) == "" {
		return ipc.DefaultSocketPath()
	}
	return filepath.Join(root, ipc.DefaultSocket), nil
}

func sendEvents(ctx context.Context, socketPath string, events []core.Event) error {
	return ipc.SendMany(ctx, socketPath, events)
}

func ensureDaemon(ctx context.Context, root, socketPath, daemonCommand string) error {
	if socketAvailable(socketPath) {
		return nil
	}
	if err := os.MkdirAll(root, tmjournal.DirMode); err != nil {
		return fmt.Errorf("create threadmark root: %w", err)
	}
	if err := os.Chmod(root, tmjournal.DirMode); err != nil {
		return fmt.Errorf("set threadmark root permissions: %w", err)
	}

	lockPath := filepath.Join(root, "daemon.lock")
	release, acquired, err := acquireStartLock(lockPath)
	if err != nil {
		return err
	}
	if !acquired {
		return waitForSocket(ctx, socketPath)
	}
	defer release()

	if socketAvailable(socketPath) {
		return nil
	}
	if err := startDaemon(root, socketPath, daemonCommand); err != nil {
		return err
	}
	return waitForSocket(ctx, socketPath)
}

func socketAvailable(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func acquireStartLock(lockPath string) (func(), bool, error) {
	if info, err := os.Stat(lockPath); err == nil && time.Since(info.ModTime()) > 30*time.Second {
		_ = os.Remove(lockPath)
	}
	file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, tmjournal.FileMode)
	if errors.Is(err, os.ErrExist) {
		return func() {}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("acquire daemon start lock: %w", err)
	}
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	_ = file.Close()
	return func() { _ = os.Remove(lockPath) }, true, nil
}

func startDaemon(root, socketPath, daemonCommand string) error {
	command := daemonCommand
	if command == "" {
		command = os.Getenv("THREADMARKD_BIN")
	}
	if command == "" {
		command = defaultDaemonCommand()
	}

	logPath := filepath.Join(root, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, tmjournal.FileMode)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}

	args := append([]string{}, strings.Fields(os.Getenv("THREADMARKD_ARGS"))...)
	args = append(args, "-root", root, "-socket", socketPath)
	cmd := exec.Command(command, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	detachDaemonProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start threadmarkd: %w", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("release threadmarkd process: %w", err)
	}
	if err := writeDaemonPID(root, pid); err != nil {
		return err
	}
	return logFile.Close()
}

func defaultDaemonCommand() string {
	exe, err := os.Executable()
	if err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "threadmarkd")
		if info, statErr := os.Stat(sibling); statErr == nil && info.Mode()&0o111 != 0 {
			return sibling
		}
	}
	return "threadmarkd"
}

func waitForSocket(ctx context.Context, socketPath string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if socketAvailable(socketPath) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for threadmark daemon: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func projectDisabled(cwd string) bool {
	path, err := disabledMarkerPath(cwd)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func sessionContext(root, cwd string, n, scan int) (string, error) {
	if n <= 0 {
		return "", nil
	}
	if scan < n {
		scan = n
	}
	snapshot := workspaceSnapshot(cwd)
	card := projectCard(cwd)
	entries, err := tmjournal.NewStore(root).LastEntries(cwd, scan)
	if err != nil {
		return "", err
	}

	selected, skipped := selectSessionEntries(entries, n)
	if snapshot == "" && card == "" && len(selected) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString(sessionContextPreamble)
	b.WriteString("\n\n")
	if snapshot != "" {
		b.WriteString(snapshot)
		b.WriteString("\n\n")
	}
	if card != "" {
		b.WriteString(card)
		b.WriteString("\n\n")
	}
	if len(entries) == 0 {
		b.WriteString("No Threadmark journal entries found for this project yet.")
	} else if len(selected) == 0 {
		fmt.Fprintf(&b, "Selected 0 of %d recent journal entries", len(entries))
		if skipped > 0 {
			fmt.Fprintf(&b, "; omitted %d obvious low-signal/no-op entries", skipped)
		}
		b.WriteString(".")
	} else {
		fmt.Fprintf(&b, "Selected %d of %d recent journal entries", len(selected), len(entries))
		if skipped > 0 {
			fmt.Fprintf(&b, "; omitted %d obvious low-signal/no-op entries", skipped)
		}
		b.WriteString(". Most recent selected entry is first.")
		for i, entry := range selected {
			fmt.Fprintf(&b, "\n\n## Entry %d\n\n", i+1)
			b.WriteString(formatStartupEntry(entry))
		}
	}
	return b.String(), nil
}

type projectCardCandidate struct {
	Path   string
	Source string
}

func projectCard(cwd string) string {
	for _, candidate := range projectCardCandidates(cwd) {
		body, truncated, ok := readProjectCard(candidate.Path)
		if !ok {
			continue
		}

		var b strings.Builder
		b.WriteString("## Project Card\n\n")
		fmt.Fprintf(&b, "source: %s\n\n", candidate.Source)
		b.WriteString(body)
		if truncated {
			fmt.Fprintf(&b, "\n\n[Threadmark truncated project card at %d characters.]", projectCardMaxRunes)
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

func projectCardCandidates(cwd string) []projectCardCandidate {
	workingDir := resolvedWorkingDirForSnapshot(cwd)
	seen := map[string]bool{}
	var candidates []projectCardCandidate
	add := func(path, source string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			return
		}
		seen[clean] = true
		candidates = append(candidates, projectCardCandidate{Path: clean, Source: source})
	}

	if envPath := strings.TrimSpace(os.Getenv(projectCardEnv)); envPath != "" {
		add(resolveProjectCardPath(workingDir, envPath), projectCardEnv+"="+envPath)
	}
	if workingDir == "" {
		return candidates
	}

	add(filepath.Join(workingDir, ".threadmark", "project-card.md"), ".threadmark/project-card.md")

	gitRoot, ok := runGitSnapshotCommand(workingDir, "rev-parse", "--show-toplevel")
	if ok && strings.TrimSpace(gitRoot) != "" {
		add(filepath.Join(gitRoot, "THREADMARK.md"), "THREADMARK.md")
		add(filepath.Join(gitRoot, ".threadmark", "project-card.md"), ".threadmark/project-card.md")
	} else {
		add(filepath.Join(workingDir, "THREADMARK.md"), "THREADMARK.md")
	}
	return candidates
}

func resolveProjectCardPath(baseDir, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	if strings.TrimSpace(baseDir) == "" {
		return value
	}
	return filepath.Join(baseDir, value)
}

func readProjectCard(path string) (string, bool, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, false
	}
	body := strings.TrimSpace(reflector.Redact(string(data)))
	if body == "" {
		return "", false, false
	}
	runes := []rune(body)
	if len(runes) <= projectCardMaxRunes {
		return body, false, true
	}
	return string(runes[:projectCardMaxRunes]), true, true
}

func workspaceSnapshot(cwd string) string {
	workingDir := resolvedWorkingDirForSnapshot(cwd)
	if workingDir == "" {
		return ""
	}
	gitRoot, ok := runGitSnapshotCommand(workingDir, "rev-parse", "--show-toplevel")
	if !ok {
		return ""
	}
	branch, _ := runGitSnapshotCommand(workingDir, "branch", "--show-current")
	head, _ := runGitSnapshotCommand(workingDir, "rev-parse", "--short", "HEAD")
	statusOutput, _ := runGitSnapshotCommand(workingDir, "status", "--short")

	statusLines := splitNonEmptyLines(statusOutput)
	statusText := "clean"
	if len(statusLines) > 0 {
		statusText = fmt.Sprintf("dirty (%d paths)", len(statusLines))
	}

	var b strings.Builder
	b.WriteString("## Workspace Snapshot\n\n")
	fmt.Fprintf(&b, "working_dir: %s\n", workingDir)
	fmt.Fprintf(&b, "git_root: %s\n", gitRoot)
	if branch != "" {
		fmt.Fprintf(&b, "branch: %s\n", branch)
	} else {
		b.WriteString("branch: detached-or-unknown\n")
	}
	if head != "" {
		fmt.Fprintf(&b, "head: %s\n", head)
	}
	fmt.Fprintf(&b, "status: %s", statusText)
	if len(statusLines) > 0 {
		b.WriteString("\n")
		for i, line := range statusLines {
			if i >= 5 {
				fmt.Fprintf(&b, "dirty_paths_more: %d\n", len(statusLines)-i)
				break
			}
			fmt.Fprintf(&b, "dirty_path: %s\n", line)
		}
	}
	return strings.TrimSpace(b.String())
}

func resolvedWorkingDirForSnapshot(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved
	}
	if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
		return abs
	}
	return ""
}

func runGitSnapshotCommand(cwd string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	commandArgs := append([]string{"-C", cwd}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(output)), true
}

func splitNonEmptyLines(value string) []string {
	rawLines := strings.Split(value, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func selectSessionEntries(entries []string, limit int) ([]string, int) {
	selected := make([]string, 0, limit)
	var skipped int
	for i := len(entries) - 1; i >= 0 && len(selected) < limit; i-- {
		entry := strings.TrimSpace(entries[i])
		if entry == "" {
			continue
		}
		if lowSignalJournalEntry(entry) {
			skipped++
			continue
		}
		selected = append(selected, entry)
	}
	return selected, skipped
}

func lowSignalJournalEntry(entry string) bool {
	normalized := strings.ToLower(entry)
	for _, marker := range lowSignalJournalMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

type startupEntryMetadata struct {
	EntryID   string
	Timestamp string
	Harness   string
	Trigger   string
}

func formatStartupEntry(entry string) string {
	metadata, body, ok := splitJournalEntryForStartup(entry)
	if !ok {
		return strings.TrimSpace(entry)
	}

	var b strings.Builder
	parts := make([]string, 0, 4)
	if metadata.EntryID != "" {
		parts = append(parts, metadata.EntryID)
	}
	if metadata.Timestamp != "" {
		parts = append(parts, metadata.Timestamp)
	}
	if metadata.Harness != "" {
		parts = append(parts, metadata.Harness)
	}
	if metadata.Trigger != "" {
		parts = append(parts, metadata.Trigger)
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "source: %s\n\n", strings.Join(parts, " | "))
	}
	b.WriteString(body)
	return strings.TrimSpace(b.String())
}

func splitJournalEntryForStartup(entry string) (startupEntryMetadata, string, bool) {
	trimmed := strings.TrimSpace(entry)
	if !strings.HasPrefix(trimmed, "---\n") {
		return startupEntryMetadata{}, "", false
	}
	withoutOpening := strings.TrimPrefix(trimmed, "---\n")
	separator := "\n---"
	idx := strings.Index(withoutOpening, separator)
	if idx < 0 {
		return startupEntryMetadata{}, "", false
	}
	frontmatter := withoutOpening[:idx]
	body := strings.TrimSpace(withoutOpening[idx+len(separator):])
	if body == "" {
		return startupEntryMetadata{}, "", false
	}
	return parseStartupEntryMetadata(frontmatter), body, true
}

func parseStartupEntryMetadata(frontmatter string) startupEntryMetadata {
	var metadata startupEntryMetadata
	for _, line := range strings.Split(frontmatter, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		parsed := parseFrontmatterStringValue(value)
		switch strings.TrimSpace(key) {
		case "entry_id":
			metadata.EntryID = parsed
		case "timestamp":
			metadata.Timestamp = parsed
		case "harness":
			metadata.Harness = parsed
		case "trigger":
			metadata.Trigger = parsed
		}
	}
	return metadata
}

func parseFrontmatterStringValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if parsed, err := strconv.Unquote(trimmed); err == nil {
		return parsed
	}
	return trimmed
}

func writeSessionStartContext(output io.Writer, hookEventName, contextText string) error {
	payload := struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}{}
	payload.HookSpecificOutput.HookEventName = hookEventName
	payload.HookSpecificOutput.AdditionalContext = contextText
	encoder := json.NewEncoder(output)
	return encoder.Encode(payload)
}

func hookFailure(root string, strict bool, stderr io.Writer, err error) int {
	if root != "" {
		_ = appendHookError(root, err)
	}
	if strict || os.Getenv("THREADMARK_HOOK_DEBUG") == "1" {
		fmt.Fprintf(stderr, "threadmark hook: %v\n", err)
	}
	if strict {
		return 1
	}
	return 0
}

func appendHookError(root string, err error) error {
	if err == nil {
		return nil
	}
	if mkErr := os.MkdirAll(root, tmjournal.DirMode); mkErr != nil {
		return mkErr
	}
	path := filepath.Join(root, "hook.log")
	file, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, tmjournal.FileMode)
	if openErr != nil {
		return openErr
	}
	defer file.Close()
	_, writeErr := fmt.Fprintf(file, "%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), err.Error())
	return writeErr
}
