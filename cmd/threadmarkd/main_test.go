package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
	"github.com/thinkwright/threadmark/internal/daemon"
	"github.com/thinkwright/threadmark/internal/journal"
	"github.com/thinkwright/threadmark/internal/reflector"
)

func TestCheckpointHandlerWritesJournalEntry(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(t.TempDir(), ".threadmark")
	promptPath := filepath.Join(t.TempDir(), "reflector.md")
	if err := os.WriteFile(promptPath, []byte("```\nsystem prompt\n```\n"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	client, err := reflector.NewClient(reflector.Config{
		PromptPath: promptPath,
		Runner:     fakeRunner{output: "> Useful line\n\nI learned something useful."},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	logFile, err := os.CreateTemp(t.TempDir(), "threadmarkd-log-*.jsonl")
	if err != nil {
		t.Fatalf("create log file: %v", err)
	}
	defer logFile.Close()

	store := journal.NewStore(root)
	handler := checkpointHandler{
		store:     store,
		reflector: client,
		logger:    newEventLogger(logFile),
	}

	checkpoint := testCheckpoint(workingDir)
	result := daemon.IngestResult{
		ProjectHash: "project",
		WorkingDir:  workingDir,
		Excerpt:     "user said: keep TOKEN=<redacted>",
		Result: core.IngestResult{
			ThreadID:   checkpoint.ThreadID,
			Checkpoint: &checkpoint,
		},
	}
	if err := handler.Handle(context.Background(), result); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	entries, err := store.LastEntries(workingDir, 1)
	if err != nil {
		t.Fatalf("LastEntries returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	assertContains(t, entries[0], "trigger: \"commit:abc123\"")
	assertContains(t, entries[0], "reflector_prompt_hash: \"sha256:")
	assertContains(t, entries[0], "> Useful line")
}

func TestCheckpointHandlerNoJournalSkipsReflector(t *testing.T) {
	workingDir := t.TempDir()
	client, err := reflector.NewClient(reflector.Config{
		PromptPath: filepath.Join(t.TempDir(), "missing.md"),
		Runner:     fakeRunner{output: "should not run"},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "threadmarkd-log-*.jsonl")
	if err != nil {
		t.Fatalf("create log file: %v", err)
	}
	defer logFile.Close()

	handler := checkpointHandler{
		store:     journal.NewStore(filepath.Join(t.TempDir(), ".threadmark")),
		reflector: client,
		logger:    newEventLogger(logFile),
		noJournal: true,
	}

	checkpoint := testCheckpoint(workingDir)
	err = handler.Handle(context.Background(), daemon.IngestResult{
		ProjectHash: "project",
		WorkingDir:  workingDir,
		Result:      core.IngestResult{ThreadID: checkpoint.ThreadID, Checkpoint: &checkpoint},
	})
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
}

func TestCheckpointHandlerSkipsEmptyExcerpt(t *testing.T) {
	workingDir := t.TempDir()
	promptPath := filepath.Join(t.TempDir(), "reflector.md")
	if err := os.WriteFile(promptPath, []byte("```\nsystem prompt\n```\n"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	client, err := reflector.NewClient(reflector.Config{
		PromptPath: promptPath,
		Runner:     fakeRunner{output: "should not run"},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "threadmarkd-log-*.jsonl")
	if err != nil {
		t.Fatalf("create log file: %v", err)
	}
	defer logFile.Close()

	store := journal.NewStore(filepath.Join(t.TempDir(), ".threadmark"))
	handler := checkpointHandler{
		store:     store,
		reflector: client,
		logger:    newEventLogger(logFile),
	}

	checkpoint := testCheckpoint(workingDir)
	err = handler.Handle(context.Background(), daemon.IngestResult{
		ProjectHash: "project",
		WorkingDir:  workingDir,
		Result:      core.IngestResult{ThreadID: checkpoint.ThreadID, Checkpoint: &checkpoint},
	})
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	entries, err := store.LastEntries(workingDir, 1)
	if err != nil {
		t.Fatalf("LastEntries returned error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0", len(entries))
	}
	logData, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	assertContains(t, string(logData), `"kind":"journal.skipped"`)
	assertContains(t, string(logData), `"reason":"empty_excerpt"`)
	if strings.Contains(string(logData), "reflector.completed") {
		t.Fatalf("reflector ran despite empty excerpt: %s", string(logData))
	}
}

func TestCheckpointHandlerTimesOutReflector(t *testing.T) {
	workingDir := t.TempDir()
	promptPath := filepath.Join(t.TempDir(), "reflector.md")
	if err := os.WriteFile(promptPath, []byte("```\nsystem prompt\n```\n"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	client, err := reflector.NewClient(reflector.Config{
		PromptPath: promptPath,
		Runner:     blockingRunner{},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "threadmarkd-log-*.jsonl")
	if err != nil {
		t.Fatalf("create log file: %v", err)
	}
	defer logFile.Close()

	handler := checkpointHandler{
		store:            journal.NewStore(filepath.Join(t.TempDir(), ".threadmark")),
		reflector:        client,
		logger:           newEventLogger(logFile),
		reflectorTimeout: 10 * time.Millisecond,
		reflectorSlots:   make(chan struct{}, 1),
	}
	checkpoint := testCheckpoint(workingDir)
	err = handler.Handle(context.Background(), daemon.IngestResult{
		ProjectHash: "project",
		WorkingDir:  workingDir,
		Excerpt:     "user said: keep going",
		Result:      core.IngestResult{ThreadID: checkpoint.ThreadID, Checkpoint: &checkpoint},
	})
	if err == nil {
		t.Fatal("Handle returned nil error")
	}
	logData, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	assertContains(t, string(logData), `"kind":"reflector.timed_out"`)
	assertContains(t, string(logData), `"kind":"reflector.failed"`)
}

func TestCheckpointHandlerBackoffWhenReflectorSlotUnavailable(t *testing.T) {
	workingDir := t.TempDir()
	promptPath := filepath.Join(t.TempDir(), "reflector.md")
	if err := os.WriteFile(promptPath, []byte("```\nsystem prompt\n```\n"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	client, err := reflector.NewClient(reflector.Config{
		PromptPath: promptPath,
		Runner:     fakeRunner{output: "should not run"},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "threadmarkd-log-*.jsonl")
	if err != nil {
		t.Fatalf("create log file: %v", err)
	}
	defer logFile.Close()

	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	handler := checkpointHandler{
		store:            journal.NewStore(filepath.Join(t.TempDir(), ".threadmark")),
		reflector:        client,
		logger:           newEventLogger(logFile),
		reflectorTimeout: 10 * time.Millisecond,
		reflectorSlots:   slots,
	}
	checkpoint := testCheckpoint(workingDir)
	err = handler.Handle(context.Background(), daemon.IngestResult{
		ProjectHash: "project",
		WorkingDir:  workingDir,
		Excerpt:     "user said: keep going",
		Result:      core.IngestResult{ThreadID: checkpoint.ThreadID, Checkpoint: &checkpoint},
	})
	if err == nil {
		t.Fatal("Handle returned nil error")
	}
	logData, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	assertContains(t, string(logData), `"kind":"reflector.backoff"`)
}

func TestHandleCheckpointSchedulesInMemoryRetry(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(t.TempDir(), ".threadmark")
	promptPath := filepath.Join(t.TempDir(), "reflector.md")
	if err := os.WriteFile(promptPath, []byte("```\nsystem prompt\n```\n"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	runner := &flakyRunner{
		failures: 1,
		output:   "> Retried line\n\nRecovered checkpoint.",
		err:      errors.New("temporary reflector failure"),
	}
	client, err := reflector.NewClient(reflector.Config{
		PromptPath: promptPath,
		Runner:     runner,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "threadmarkd-log-*.jsonl")
	if err != nil {
		t.Fatalf("create log file: %v", err)
	}
	defer logFile.Close()

	store := journal.NewStore(root)
	ingestor := daemon.NewIngestor(store, core.Config{})
	logger := newEventLogger(logFile)
	handler := checkpointHandler{
		store:            store,
		reflector:        client,
		logger:           logger,
		reflectorTimeout: time.Second,
		reflectorSlots:   make(chan struct{}, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	retries := newCheckpointRetrier(ctx, ingestor, handler, logger, time.Millisecond, 2*time.Millisecond)

	checkpoint := testCheckpoint(workingDir)
	result := daemon.IngestResult{
		ProjectHash: "project",
		WorkingDir:  workingDir,
		Excerpt:     "user said: retry this checkpoint",
		Result: core.IngestResult{
			ThreadID:   checkpoint.ThreadID,
			Checkpoint: &checkpoint,
		},
	}
	if err := handleCheckpoint(ctx, ingestor, handler, retries, result); err != nil {
		t.Fatalf("handleCheckpoint returned error: %v", err)
	}
	if entries, err := store.LastEntries(workingDir, 1); err != nil {
		t.Fatalf("LastEntries returned error: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("journal entries immediately after failed first attempt = %d, want 0", len(entries))
	}

	entries := waitForEntries(t, store, workingDir, 1)
	assertContains(t, entries[0], "> Retried line")

	logData, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	assertContains(t, string(logData), `"kind":"checkpoint.retry_scheduled"`)
	assertContains(t, string(logData), `"kind":"checkpoint.retry_completed"`)
	if got, want := runner.Calls(), 2; got != want {
		t.Fatalf("runner calls = %d, want %d", got, want)
	}
}

type fakeRunner struct {
	output string
}

func (r fakeRunner) Run(context.Context, string, []string) (string, error) {
	return r.output, nil
}

type flakyRunner struct {
	mu       sync.Mutex
	calls    int
	failures int
	output   string
	err      error
}

func (r *flakyRunner) Run(context.Context, string, []string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls++
	if r.calls <= r.failures {
		return "", r.err
	}
	return r.output, nil
}

func (r *flakyRunner) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type blockingRunner struct{}

func (blockingRunner) Run(ctx context.Context, _ string, _ []string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func testCheckpoint(workingDir string) core.CheckpointRequest {
	ts := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	return core.CheckpointRequest{
		Trigger: core.TriggerCandidate{
			Type:   core.TriggerCommit,
			Reason: "commit:abc123",
			At:     ts,
		},
		ThreadID:        "t_test",
		TranscriptID:    "transcript-1",
		Harness:         "codex",
		WorkingDir:      workingDir,
		SourceRange:     core.SourceEventRange{First: "evt_1", Last: "evt_2"},
		SourceStartedAt: ts.Add(-time.Minute),
		SourceEndedAt:   ts,
		CommitRefs:      []string{"abc123"},
	}
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}

func waitForEntries(t *testing.T, store journal.Store, workingDir string, want int) []string {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		entries, err := store.LastEntries(workingDir, want)
		if err != nil {
			t.Fatalf("LastEntries returned error: %v", err)
		}
		if len(entries) == want {
			return entries
		}
		time.Sleep(10 * time.Millisecond)
	}

	entries, err := store.LastEntries(workingDir, want)
	if err != nil {
		t.Fatalf("LastEntries returned error: %v", err)
	}
	t.Fatalf("len(entries) = %d, want %d", len(entries), want)
	return nil
}
