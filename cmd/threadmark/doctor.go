package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tmjournal "github.com/thinkwright/threadmark/internal/journal"
	"github.com/thinkwright/threadmark/internal/reflector"
)

type doctorSeverity string

const (
	doctorOK   doctorSeverity = "ok"
	doctorWarn doctorSeverity = "warn"
	doctorFail doctorSeverity = "fail"
)

type doctorReport struct {
	Root       string        `json:"root"`
	Socket     string        `json:"socket"`
	WorkingDir string        `json:"working_dir"`
	Summary    doctorSummary `json:"summary"`
	Checks     []doctorCheck `json:"checks"`
}

type doctorSummary struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

type doctorCheck struct {
	Name   string         `json:"name"`
	Status doctorSeverity `json:"status"`
	Detail string         `json:"detail"`
	Fix    string         `json:"fix,omitempty"`
}

func doctorCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workingDir := flags.String("working-dir", ".", "project working directory")
	rootFlag := flags.String("root", "", "threadmark root directory (default ~/.threadmark)")
	socketFlag := flags.String("socket", "", "unix socket path (default <root>/daemon.sock)")
	claudeSettings := flags.String("claude-settings", "", "Claude Code settings file to inspect (default .claude/settings.json)")
	codexHooks := flags.String("codex-hooks", "", "Codex hooks file to inspect (default .codex/hooks.json)")
	reflectorCommand := flags.String("reflector-command", reflector.DefaultCommand, "reflector CLI command to look up")
	reflectorPrompt := flags.String("reflector-prompt", reflector.DefaultPromptPath(), "reflector prompt path to inspect")
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	report, err := buildDoctorReport(doctorOptions{
		WorkingDir:       *workingDir,
		RootFlag:         *rootFlag,
		SocketFlag:       *socketFlag,
		ClaudeSettings:   *claudeSettings,
		CodexHooks:       *codexHooks,
		ReflectorCommand: *reflectorCommand,
		ReflectorPrompt:  *reflectorPrompt,
	})
	if err != nil {
		fmt.Fprintf(stderr, "threadmark doctor: %v\n", err)
		return 1
	}

	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "threadmark doctor: %v\n", err)
			return 1
		}
	} else {
		printDoctorReport(stdout, report)
	}

	if report.Summary.Fail > 0 {
		return 1
	}
	return 0
}

type doctorOptions struct {
	WorkingDir       string
	RootFlag         string
	SocketFlag       string
	ClaudeSettings   string
	CodexHooks       string
	ReflectorCommand string
	ReflectorPrompt  string
}

func buildDoctorReport(opts doctorOptions) (doctorReport, error) {
	root, err := resolveRoot(opts.RootFlag)
	if err != nil {
		return doctorReport{}, err
	}
	socketPath, err := resolveSocket(root, opts.SocketFlag)
	if err != nil {
		return doctorReport{}, err
	}
	_, resolvedWorkingDir, workingDirErr := tmjournal.NewStore(root).ProjectID(opts.WorkingDir)

	report := doctorReport{
		Root:       root,
		Socket:     socketPath,
		WorkingDir: resolvedWorkingDir,
	}
	add := func(name string, status doctorSeverity, detail, fix string) {
		report.Checks = append(report.Checks, doctorCheck{
			Name:   name,
			Status: status,
			Detail: detail,
			Fix:    fix,
		})
	}

	checkRoot(add, root)
	checkDaemon(add, root, socketPath)
	if workingDirErr != nil {
		add("working_dir", doctorFail, workingDirErr.Error(), "run doctor from a valid project directory or pass --working-dir")
	} else {
		add("working_dir", doctorOK, resolvedWorkingDir, "")
		checkProject(add, root, resolvedWorkingDir)
		checkProjectDisabled(add, resolvedWorkingDir)
	}
	checkClaudeHooks(add, resolvedWorkingDir, opts.ClaudeSettings)
	checkCodexHooks(add, resolvedWorkingDir, opts.CodexHooks)
	checkReflector(add, opts.ReflectorCommand, opts.ReflectorPrompt)

	report.Summary = summarizeDoctorChecks(report.Checks)
	return report, nil
}

func checkRoot(add func(string, doctorSeverity, string, string), root string) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		add("root", doctorWarn, fmt.Sprintf("%s is missing", root), "run `threadmark activate` in a project or start an activated agent session")
		return
	}
	if err != nil {
		add("root", doctorFail, fmt.Sprintf("stat %s: %v", root, err), "check filesystem permissions")
		return
	}
	if !info.IsDir() {
		add("root", doctorFail, fmt.Sprintf("%s is not a directory", root), "move the file aside and restart Threadmark")
		return
	}
	if info.Mode().Perm()&0o077 != 0 {
		add("root_permissions", doctorFail, fmt.Sprintf("%s mode is %s, want private 0700-style permissions", root, modeString(info.Mode().Perm())), fmt.Sprintf("chmod 700 %s", root))
		return
	}
	add("root", doctorOK, fmt.Sprintf("%s (%s)", root, modeString(info.Mode().Perm())), "")
}

func checkDaemon(add func(string, doctorSeverity, string, string), root, socketPath string) {
	reachable, daemonErr := daemonReachable(socketPath)
	pid, pidErr := readDaemonPID(root)
	switch {
	case reachable && pidErr == nil:
		add("daemon", doctorOK, fmt.Sprintf("reachable pid=%d socket=%s", pid, socketPath), "")
	case reachable:
		add("daemon", doctorWarn, fmt.Sprintf("reachable socket=%s pid=unknown (%v)", socketPath, pidErr), "restart with `threadmark daemon restart` to refresh the PID file")
	case pidErr == nil:
		add("daemon", doctorFail, fmt.Sprintf("socket unreachable with stale pid=%d: %s", pid, daemonErr), "run `threadmark daemon restart`")
	default:
		add("daemon", doctorWarn, fmt.Sprintf("not running yet: %s", daemonErr), "run `threadmark activate` in this project or start an activated agent session")
	}

	logPath := filepath.Join(root, "daemon.log")
	if info, err := os.Stat(logPath); err == nil {
		if info.Mode().Perm()&0o077 != 0 {
			add("daemon_log_permissions", doctorWarn, fmt.Sprintf("%s mode is %s, want private 0600-style permissions", logPath, modeString(info.Mode().Perm())), fmt.Sprintf("chmod 600 %s", logPath))
		} else {
			add("daemon_log", doctorOK, fmt.Sprintf("%s (%d bytes)", logPath, info.Size()), "")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		add("daemon_log", doctorWarn, fmt.Sprintf("%s is missing", logPath), "activate a project or start an activated agent session")
	} else {
		add("daemon_log", doctorWarn, fmt.Sprintf("stat %s: %v", logPath, err), "check filesystem permissions")
	}
}

func checkProject(add func(string, doctorSeverity, string, string), root, workingDir string) {
	store := tmjournal.NewStore(root)
	projectDir, _, err := store.ProjectDir(workingDir)
	if err != nil {
		add("project", doctorFail, err.Error(), "check --working-dir")
		return
	}
	add("project", doctorOK, projectDir, "")

	statePath, _, err := store.StatePath(workingDir)
	if err != nil {
		add("state", doctorFail, err.Error(), "check --working-dir")
		return
	}
	state, exists, err := readProjectState(statePath)
	if err != nil {
		add("state", doctorFail, fmt.Sprintf("%s: %v", statePath, err), "inspect or remove the corrupt state file")
	} else if !exists {
		add("state", doctorWarn, fmt.Sprintf("%s is missing", statePath), "send a hook event in this project")
	} else if state.Debounce.PendingTrigger != nil {
		add("state", doctorWarn, fmt.Sprintf("%s has pending trigger %q; pending triggers are transient across daemon restarts", statePath, state.Debounce.PendingTrigger.Reason), "let the daemon flush it or restart to drop transient pending state")
	} else {
		add("state", doctorOK, fmt.Sprintf("%s thread=%s", statePath, state.CurrentThreadID), "")
	}
	checkPrivateFile(add, "state_permissions", statePath)

	journalPath, _, err := store.JournalPath(workingDir)
	if err != nil {
		add("journal", doctorFail, err.Error(), "check --working-dir")
		return
	}
	entries, journalExists, err := countJournalEntries(journalPath)
	if err != nil {
		add("journal", doctorFail, fmt.Sprintf("%s: %v", journalPath, err), "check journal permissions")
	} else if !journalExists {
		add("journal", doctorWarn, fmt.Sprintf("%s is missing", journalPath), "complete a journal-on checkpoint")
	} else {
		add("journal", doctorOK, fmt.Sprintf("%s entries=%d", journalPath, entries), "")
	}
	checkPrivateFile(add, "journal_permissions", journalPath)
}

func checkPrivateFile(add func(string, doctorSeverity, string, string), name, path string) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		add(name, doctorWarn, fmt.Sprintf("stat %s: %v", path, err), "check filesystem permissions")
		return
	}
	if info.Mode().Perm()&0o077 != 0 {
		add(name, doctorWarn, fmt.Sprintf("%s mode is %s, want private 0600-style permissions", path, modeString(info.Mode().Perm())), fmt.Sprintf("chmod 600 %s", path))
	}
}

func checkProjectDisabled(add func(string, doctorSeverity, string, string), workingDir string) {
	path, err := disabledMarkerPath(workingDir)
	if err != nil {
		add("project_enabled", doctorFail, err.Error(), "check --working-dir")
		return
	}
	_, err = os.Stat(path)
	if err == nil {
		add("project_enabled", doctorWarn, fmt.Sprintf("%s exists; hooks will skip this project", path), fmt.Sprintf("rm %s", path))
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		add("project_enabled", doctorOK, "no project disabled marker", "")
		return
	}
	add("project_enabled", doctorWarn, fmt.Sprintf("stat %s: %v", path, err), "check filesystem permissions")
}

func checkClaudeHooks(add func(string, doctorSeverity, string, string), workingDir, explicit string) {
	path := explicit
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(workingDir, ".claude", "settings.json")
	}
	checkHookFile(add, "claude_hooks", path, []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "PreCompact", "PostCompact"}, "run `threadmark activate` from this project")
}

func checkCodexHooks(add func(string, doctorSeverity, string, string), workingDir, explicit string) {
	path := explicit
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(workingDir, ".codex", "hooks.json")
	}
	checkHookFile(add, "codex_hooks", path, []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "PreCompact", "PostCompact"}, "run `threadmark activate` from this project, then run /hooks in Codex")
}

func checkHookFile(add func(string, doctorSeverity, string, string), name, path string, events []string, fix string) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		add(name, doctorWarn, fmt.Sprintf("%s is missing", path), fix)
		return
	}
	if err != nil {
		add(name, doctorFail, fmt.Sprintf("read %s: %v", path, err), "check filesystem permissions")
		return
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		add(name, doctorFail, fmt.Sprintf("decode %s: %v", path, err), "fix JSON or reinstall hooks")
		return
	}
	hooks := mapValue(settings, "hooks")
	missing := make([]string, 0)
	for _, event := range events {
		if !hookGroupsContainThreadmark(arrayValue(hooks, event)) {
			missing = append(missing, event)
		}
	}
	if len(missing) > 0 {
		add(name, doctorWarn, fmt.Sprintf("%s missing Threadmark hooks for %s", path, strings.Join(missing, ", ")), fix)
		return
	}
	add(name, doctorOK, path, "")
}

func hookGroupsContainThreadmark(groups []any) bool {
	for _, group := range groups {
		groupMap, ok := group.(map[string]any)
		if !ok {
			continue
		}
		for _, hook := range arrayFromAny(groupMap["hooks"]) {
			if hookContainsThreadmark(hook) {
				return true
			}
		}
	}
	return false
}

func hookContainsThreadmark(hook any) bool {
	hookMap, ok := hook.(map[string]any)
	if !ok {
		return false
	}
	values := []string{fmt.Sprint(hookMap["command"])}
	for _, arg := range stringSliceValue(hookMap["args"]) {
		values = append(values, arg)
	}
	return strings.Contains(strings.Join(values, " "), "threadmark")
}

func checkReflector(add func(string, doctorSeverity, string, string), command, promptPath string) {
	mode := os.Getenv("THREADMARK_REFLECTOR_MODE")
	if _, err := reflector.ParseMode(mode); err != nil {
		add("reflector_mode", doctorFail, err.Error(), "set THREADMARK_REFLECTOR_MODE=convenience or bare")
	} else if strings.TrimSpace(mode) == "" {
		add("reflector_mode", doctorOK, "convenience (default)", "")
	} else {
		add("reflector_mode", doctorOK, strings.TrimSpace(mode), "")
	}

	if strings.TrimSpace(command) == "" {
		command = reflector.DefaultCommand
	}
	if path, err := exec.LookPath(command); err == nil {
		add("reflector_command", doctorOK, fmt.Sprintf("%s -> %s", command, path), "")
	} else {
		add("reflector_command", doctorFail, fmt.Sprintf("%s not found: %v", command, err), "install/configure Claude CLI or pass --reflector-command")
	}

	if strings.TrimSpace(promptPath) == "" {
		promptPath = reflector.DefaultPromptPath()
	}
	prompt, err := reflector.LoadPromptFile(promptPath)
	if err != nil {
		add("reflector_prompt", doctorFail, fmt.Sprintf("%s: %v", promptPath, err), "restore prompts/reflector.md or pass --reflector-prompt")
		return
	}
	if strings.TrimSpace(prompt) == "" {
		add("reflector_prompt", doctorFail, fmt.Sprintf("%s loaded empty prompt", promptPath), "restore prompt contents")
		return
	}
	add("reflector_prompt", doctorOK, fmt.Sprintf("%s hash=%s", promptPath, reflector.PromptHash(prompt)), "")
}

func summarizeDoctorChecks(checks []doctorCheck) doctorSummary {
	var summary doctorSummary
	for _, check := range checks {
		switch check.Status {
		case doctorOK:
			summary.OK++
		case doctorWarn:
			summary.Warn++
		case doctorFail:
			summary.Fail++
		}
	}
	return summary
}

func printDoctorReport(output io.Writer, report doctorReport) {
	fmt.Fprintf(output, "Threadmark doctor: %d ok, %d warn, %d fail\n", report.Summary.OK, report.Summary.Warn, report.Summary.Fail)
	fmt.Fprintf(output, "root: %s\n", report.Root)
	fmt.Fprintf(output, "socket: %s\n", report.Socket)
	if report.WorkingDir != "" {
		fmt.Fprintf(output, "working_dir: %s\n", report.WorkingDir)
	}
	fmt.Fprintln(output)
	for _, check := range report.Checks {
		fmt.Fprintf(output, "[%s] %s: %s\n", strings.ToUpper(string(check.Status)), check.Name, check.Detail)
		if check.Fix != "" {
			fmt.Fprintf(output, "      fix: %s\n", check.Fix)
		}
	}
}

func modeString(mode os.FileMode) string {
	return fmt.Sprintf("%04o", mode.Perm())
}
