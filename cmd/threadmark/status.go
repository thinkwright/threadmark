package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
	tmjournal "github.com/thinkwright/threadmark/internal/journal"
)

type statusReport struct {
	Root       string          `json:"root"`
	Socket     string          `json:"socket"`
	Daemon     daemonStatus    `json:"daemon"`
	Project    projectStatus   `json:"project"`
	RecentLogs []daemonLogLine `json:"recent_logs,omitempty"`
	Generated  time.Time       `json:"generated_at"`
}

type daemonStatus struct {
	Reachable bool   `json:"reachable"`
	PID       int    `json:"pid,omitempty"`
	Error     string `json:"error,omitempty"`
	LogPath   string `json:"log_path"`
}

type projectStatus struct {
	ID                   string         `json:"id"`
	WorkingDir           string         `json:"working_dir"`
	ProjectDir           string         `json:"project_dir"`
	StatePath            string         `json:"state_path"`
	StateExists          bool           `json:"state_exists"`
	JournalPath          string         `json:"journal_path"`
	JournalExists        bool           `json:"journal_exists"`
	JournalEntries       int            `json:"journal_entries"`
	CurrentThreadID      string         `json:"current_thread_id,omitempty"`
	LastHarness          string         `json:"last_harness,omitempty"`
	LastTranscriptID     string         `json:"last_transcript_id,omitempty"`
	LastActivityTS       *time.Time     `json:"last_activity_ts,omitempty"`
	LastCheckpointTS     *time.Time     `json:"last_checkpoint_ts,omitempty"`
	TurnsSinceCheckpoint int            `json:"turns_since_checkpoint,omitempty"`
	SubstantiveEvents    int            `json:"substantive_events_since_checkpoint,omitempty"`
	PendingTrigger       *triggerStatus `json:"pending_trigger,omitempty"`
}

type triggerStatus struct {
	Type     core.TriggerType `json:"type"`
	Reason   string           `json:"reason"`
	Priority int              `json:"priority"`
	EventID  string           `json:"event_id,omitempty"`
	At       time.Time        `json:"at"`
}

type daemonLogLine struct {
	Raw       json.RawMessage `json:"raw"`
	TS        string          `json:"ts,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	ProjectID string          `json:"project_id,omitempty"`
	ThreadID  string          `json:"thread_id,omitempty"`
	Harness   string          `json:"harness,omitempty"`
	EventKind string          `json:"event_kind,omitempty"`
	Trigger   string          `json:"trigger,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func statusCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workingDir := flags.String("working-dir", ".", "project working directory")
	rootFlag := flags.String("root", "", "threadmark root directory (default ~/.threadmark)")
	socketFlag := flags.String("socket", "", "unix socket path (default <root>/daemon.sock)")
	last := flags.Int("last", 10, "number of recent daemon log lines to show")
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	follow := flags.Bool("follow", false, "follow daemon log after printing status")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	report, err := buildStatusReport(*workingDir, *rootFlag, *socketFlag, *last)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark status: %v\n", err)
		return 1
	}

	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "threadmark status: %v\n", err)
			return 1
		}
	} else {
		printStatus(stdout, report)
	}

	if *follow {
		if err := followLog(stdout, report.Daemon.LogPath); err != nil {
			fmt.Fprintf(stderr, "threadmark status: %v\n", err)
			return 1
		}
	}

	return 0
}

func buildStatusReport(workingDir, rootFlag, socketFlag string, last int) (statusReport, error) {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return statusReport{}, err
	}
	socketPath, err := resolveSocket(root, socketFlag)
	if err != nil {
		return statusReport{}, err
	}

	store := tmjournal.NewStore(root)
	projectDir, resolvedWorkingDir, err := store.ProjectDir(workingDir)
	if err != nil {
		return statusReport{}, err
	}
	projectID, _, err := store.ProjectID(resolvedWorkingDir)
	if err != nil {
		return statusReport{}, err
	}
	statePath, _, err := store.StatePath(resolvedWorkingDir)
	if err != nil {
		return statusReport{}, err
	}
	journalPath, _, err := store.JournalPath(resolvedWorkingDir)
	if err != nil {
		return statusReport{}, err
	}

	state, stateExists, err := readProjectState(statePath)
	if err != nil {
		return statusReport{}, err
	}
	journalEntries, journalExists, err := countJournalEntries(journalPath)
	if err != nil {
		return statusReport{}, err
	}
	logPath := filepath.Join(root, "daemon.log")
	logs, err := readRecentLogLines(logPath, last)
	if err != nil {
		return statusReport{}, err
	}
	reachable, daemonErr := daemonReachable(socketPath)
	pid, pidErr := readDaemonPID(root)
	if pidErr != nil {
		pid = 0
	}

	return statusReport{
		Root:   root,
		Socket: socketPath,
		Daemon: daemonStatus{
			Reachable: reachable,
			PID:       pid,
			Error:     daemonErr,
			LogPath:   logPath,
		},
		Project: projectStatus{
			ID:                   projectID,
			WorkingDir:           resolvedWorkingDir,
			ProjectDir:           projectDir,
			StatePath:            statePath,
			StateExists:          stateExists,
			JournalPath:          journalPath,
			JournalExists:        journalExists,
			JournalEntries:       journalEntries,
			CurrentThreadID:      state.CurrentThreadID,
			LastHarness:          state.LastHarness,
			LastTranscriptID:     state.LastTranscriptID,
			LastActivityTS:       timePointer(state.LastActivityTS),
			LastCheckpointTS:     timePointer(state.LastCheckpointTS),
			TurnsSinceCheckpoint: state.TurnsSinceCheckpoint,
			SubstantiveEvents:    state.SubstantiveEvents,
			PendingTrigger:       pendingTriggerStatus(state.Debounce.PendingTrigger),
		},
		RecentLogs: logs,
		Generated:  time.Now().UTC(),
	}, nil
}

func printStatus(output io.Writer, report statusReport) {
	fmt.Fprintf(output, "root: %s\n", report.Root)
	fmt.Fprintf(output, "socket: %s\n", report.Socket)
	if report.Daemon.Reachable {
		if report.Daemon.PID > 0 {
			fmt.Fprintf(output, "daemon: reachable (pid=%d)\n", report.Daemon.PID)
		} else {
			fmt.Fprintln(output, "daemon: reachable")
		}
	} else if report.Daemon.Error != "" {
		fmt.Fprintf(output, "daemon: offline (%s)\n", report.Daemon.Error)
	} else {
		fmt.Fprintln(output, "daemon: offline")
	}
	fmt.Fprintf(output, "daemon_log: %s\n", report.Daemon.LogPath)
	fmt.Fprintln(output)

	fmt.Fprintln(output, "project:")
	fmt.Fprintf(output, "  id: %s\n", report.Project.ID)
	fmt.Fprintf(output, "  working_dir: %s\n", report.Project.WorkingDir)
	fmt.Fprintf(output, "  project_dir: %s\n", report.Project.ProjectDir)
	fmt.Fprintf(output, "  state: %s (%s)\n", report.Project.StatePath, present(report.Project.StateExists))
	fmt.Fprintf(output, "  journal: %s (%s, entries=%d)\n", report.Project.JournalPath, present(report.Project.JournalExists), report.Project.JournalEntries)
	if report.Project.CurrentThreadID != "" {
		fmt.Fprintf(output, "  current_thread: %s\n", report.Project.CurrentThreadID)
	}
	if report.Project.LastHarness != "" {
		fmt.Fprintf(output, "  last_harness: %s\n", report.Project.LastHarness)
	}
	if report.Project.LastTranscriptID != "" {
		fmt.Fprintf(output, "  last_transcript: %s\n", report.Project.LastTranscriptID)
	}
	if report.Project.LastActivityTS != nil {
		fmt.Fprintf(output, "  last_activity: %s\n", report.Project.LastActivityTS.Format(time.RFC3339Nano))
	}
	if report.Project.LastCheckpointTS != nil {
		fmt.Fprintf(output, "  last_checkpoint: %s\n", report.Project.LastCheckpointTS.Format(time.RFC3339Nano))
	}
	if report.Project.TurnsSinceCheckpoint != 0 {
		fmt.Fprintf(output, "  turns_since_checkpoint: %d\n", report.Project.TurnsSinceCheckpoint)
	}
	if report.Project.SubstantiveEvents != 0 {
		fmt.Fprintf(output, "  substantive_events_since_checkpoint: %d\n", report.Project.SubstantiveEvents)
	}
	if report.Project.PendingTrigger != nil {
		trigger := report.Project.PendingTrigger
		fmt.Fprintf(output, "  pending_trigger: %s (%s, priority=%d)\n", trigger.Reason, trigger.Type, trigger.Priority)
	}

	if len(report.RecentLogs) > 0 {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "recent_log:")
		for _, line := range report.RecentLogs {
			fmt.Fprintf(output, "  %s\n", summarizeLogLine(line))
		}
	}
}

func readProjectState(path string) (core.ProjectState, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return core.ProjectState{}, false, nil
	}
	if err != nil {
		return core.ProjectState{}, false, fmt.Errorf("read state: %w", err)
	}
	var state core.ProjectState
	if err := json.Unmarshal(data, &state); err != nil {
		return core.ProjectState{}, false, fmt.Errorf("decode state: %w", err)
	}
	return state.Normalize(), true, nil
}

func countJournalEntries(path string) (int, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read journal: %w", err)
	}
	return len(tmjournal.SplitEntries(data)), true, nil
}

func readRecentLogLines(path string, n int) ([]daemonLogLine, error) {
	if n <= 0 {
		return nil, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open daemon log: %w", err)
	}
	defer file.Close()

	ring := make([]string, n)
	var count int
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		ring[count%n] = scanner.Text()
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read daemon log: %w", err)
	}

	size := count
	if size > n {
		size = n
	}
	lines := make([]daemonLogLine, 0, size)
	start := count - size
	for i := 0; i < size; i++ {
		raw := ring[(start+i)%n]
		lines = append(lines, parseDaemonLogLine(raw))
	}
	return lines, nil
}

func parseDaemonLogLine(line string) daemonLogLine {
	parsed := daemonLogLine{Raw: json.RawMessage(line)}
	var fields map[string]any
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		parsed.Raw = json.RawMessage(strconv.Quote(line))
		return parsed
	}
	parsed.TS = stringField(fields, "ts")
	parsed.Kind = stringField(fields, "kind")
	parsed.ProjectID = stringField(fields, "project_id")
	parsed.ThreadID = stringField(fields, "thread_id")
	parsed.Harness = stringField(fields, "harness")
	parsed.EventKind = stringField(fields, "event_kind")
	parsed.Trigger = stringField(fields, "trigger")
	parsed.Reason = stringField(fields, "reason")
	parsed.Error = stringField(fields, "error")
	return parsed
}

func daemonReachable(socketPath string) (bool, string) {
	conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
	if err != nil {
		return false, err.Error()
	}
	_ = conn.Close()
	return true, ""
}

func followLog(output io.Writer, path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		for {
			time.Sleep(500 * time.Millisecond)
			file, err = os.Open(path)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("open daemon log: %w", err)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek daemon log: %w", err)
	}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == nil {
			fmt.Fprint(output, line)
			continue
		}
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("read daemon log: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func pendingTriggerStatus(candidate *core.TriggerCandidate) *triggerStatus {
	if candidate == nil {
		return nil
	}
	normalized := candidate.Normalize()
	return &triggerStatus{
		Type:     normalized.Type,
		Reason:   normalized.Reason,
		Priority: normalized.Priority,
		EventID:  normalized.EventID,
		At:       normalized.At,
	}
}

func timePointer(ts time.Time) *time.Time {
	if ts.IsZero() {
		return nil
	}
	normalized := ts.UTC()
	return &normalized
}

func present(exists bool) string {
	if exists {
		return "present"
	}
	return "missing"
}

func summarizeLogLine(line daemonLogLine) string {
	parts := make([]string, 0, 6)
	if line.TS != "" {
		parts = append(parts, line.TS)
	}
	if line.Kind != "" {
		parts = append(parts, line.Kind)
	}
	if line.Harness != "" {
		parts = append(parts, "harness="+line.Harness)
	}
	if line.EventKind != "" {
		parts = append(parts, "event="+line.EventKind)
	}
	if line.Trigger != "" {
		parts = append(parts, "trigger="+line.Trigger)
	}
	if line.Reason != "" {
		parts = append(parts, "reason="+line.Reason)
	}
	if line.Error != "" {
		parts = append(parts, "error="+line.Error)
	}
	if len(parts) == 0 {
		return string(line.Raw)
	}
	return strings.Join(parts, " ")
}

func stringField(fields map[string]any, key string) string {
	value, ok := fields[key].(string)
	if !ok {
		return ""
	}
	return value
}
