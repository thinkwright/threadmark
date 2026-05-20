package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
	"github.com/thinkwright/threadmark/internal/journal"
)

func TestIngestorPersistsProjectState(t *testing.T) {
	workingDir := t.TempDir()
	store := journal.NewStore(filepath.Join(t.TempDir(), ".threadmark"))
	ingestor := NewIngestor(store, core.Config{NewThreadID: deterministicThreadIDs()})

	result, err := ingestor.Ingest(context.Background(), textEvent(t, workingDir, "evt_1", at(0), "start"))
	if err != nil {
		t.Fatalf("Ingest returned error: %v", err)
	}
	if result.ProjectHash == "" {
		t.Fatal("project hash is empty")
	}
	if result.Result.ThreadID != "t_1" {
		t.Fatalf("thread id = %s, want t_1", result.Result.ThreadID)
	}

	statePath, _, err := store.StatePath(workingDir)
	if err != nil {
		t.Fatalf("StatePath returned error: %v", err)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if got, want := info.Mode().Perm(), journal.FileMode; got != want {
		t.Fatalf("state mode = %v, want %v", got, want)
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state core.ProjectState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if got := state.TranscriptToThread["transcript-1"]; got != "t_1" {
		t.Fatalf("transcript mapping = %s, want t_1", got)
	}
}

func TestIngestorDoesNotAppendDuplicateEventsToBuffer(t *testing.T) {
	workingDir := t.TempDir()
	store := journal.NewStore(filepath.Join(t.TempDir(), ".threadmark"))
	ingestor := NewIngestor(store, core.Config{NewThreadID: deterministicThreadIDs()})
	event := textEvent(t, workingDir, "evt_1", at(0), "start")

	first, err := ingestor.Ingest(context.Background(), event)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if first.Result.Duplicate {
		t.Fatal("first ingest was marked duplicate")
	}
	if got, want := len(ingestor.buffers[first.ProjectHash]), 1; got != want {
		t.Fatalf("buffer length after first ingest = %d, want %d", got, want)
	}

	duplicate, err := ingestor.Ingest(context.Background(), event)
	if err != nil {
		t.Fatalf("duplicate ingest: %v", err)
	}
	if !duplicate.Result.Duplicate {
		t.Fatal("duplicate ingest was not marked duplicate")
	}
	if got, want := len(ingestor.buffers[first.ProjectHash]), 1; got != want {
		t.Fatalf("buffer length after duplicate = %d, want %d", got, want)
	}
}

func TestIngestorLoadsExistingState(t *testing.T) {
	workingDir := t.TempDir()
	store := journal.NewStore(filepath.Join(t.TempDir(), ".threadmark"))

	first := NewIngestor(store, core.Config{NewThreadID: deterministicThreadIDs()})
	if _, err := first.Ingest(context.Background(), textEvent(t, workingDir, "evt_1", at(0), "start")); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	second := NewIngestor(store, core.Config{NewThreadID: deterministicThreadIDsFrom(1)})
	result, err := second.Ingest(context.Background(), textEvent(t, workingDir, "evt_2", at(time.Minute), "continue"))
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if result.Result.ThreadBoundary != nil {
		t.Fatalf("unexpected thread boundary: %#v", result.Result.ThreadBoundary)
	}
	if result.Result.ThreadID != "t_1" {
		t.Fatalf("thread id = %s, want t_1", result.Result.ThreadID)
	}
}

func TestIngestorDropsPendingCheckpointStateAfterRestart(t *testing.T) {
	workingDir := t.TempDir()
	store := journal.NewStore(filepath.Join(t.TempDir(), ".threadmark"))

	first := NewIngestor(store, core.Config{
		NewThreadID:    deterministicThreadIDs(),
		DebounceWindow: time.Minute,
	})
	if _, err := first.Ingest(context.Background(), textEvent(t, workingDir, "evt_1", at(0), "substantive work")); err != nil {
		t.Fatalf("first user ingest: %v", err)
	}
	if _, err := first.Ingest(context.Background(), stopEvent(t, workingDir, "evt_stop", at(10*time.Second))); err != nil {
		t.Fatalf("first stop ingest: %v", err)
	}

	second := NewIngestor(store, core.Config{
		NewThreadID:    deterministicThreadIDsFrom(1),
		DebounceWindow: time.Minute,
	})
	result, err := second.Ingest(context.Background(), textEvent(t, workingDir, "evt_after_restart", at(2*time.Minute), "new work after restart"))
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if result.Result.Checkpoint != nil {
		t.Fatalf("checkpoint = %#v, want nil because pending excerpt buffer was not persisted", result.Result.Checkpoint)
	}
	if result.Excerpt != "" {
		t.Fatalf("excerpt = %q, want empty without checkpoint", result.Excerpt)
	}
	if result.Result.State.CurrentRangeStartID != "evt_after_restart" {
		t.Fatalf("current range start = %q, want evt_after_restart", result.Result.State.CurrentRangeStartID)
	}
	if result.Result.State.Debounce.PendingTrigger != nil {
		t.Fatalf("pending trigger survived restart: %#v", result.Result.State.Debounce.PendingTrigger)
	}
}

func TestIngestorCheckpointExcerptIsRedactedAndRawIsIgnored(t *testing.T) {
	workingDir := t.TempDir()
	store := journal.NewStore(filepath.Join(t.TempDir(), ".threadmark"))
	ingestor := NewIngestor(store, core.Config{
		NewThreadID:    deterministicThreadIDs(),
		DebounceWindow: time.Minute,
	})

	event := textEvent(t, workingDir, "evt_1", at(0), "/checkpoint PASSWORD=hunter2")
	event.Raw = []byte(`{"secret":"hunter2"}`)
	result, err := ingestor.Ingest(context.Background(), event)
	if err != nil {
		t.Fatalf("Ingest returned error: %v", err)
	}
	if result.Excerpt != "" {
		t.Fatalf("excerpt before checkpoint = %q, want empty", result.Excerpt)
	}

	checkpoint, err := ingestor.FlushDue(context.Background(), workingDir, at(11*time.Minute))
	if err != nil {
		t.Fatalf("FlushDue returned error: %v", err)
	}
	if checkpoint == nil {
		t.Fatal("expected checkpoint")
	}
	if contains(checkpoint.Excerpt, "hunter2") {
		t.Fatalf("checkpoint excerpt leaked secret: %s", checkpoint.Excerpt)
	}
	if contains(checkpoint.Excerpt, "secret") {
		t.Fatalf("checkpoint excerpt included raw payload: %s", checkpoint.Excerpt)
	}
	if !contains(checkpoint.Excerpt, "PASSWORD=<redacted>") {
		t.Fatalf("checkpoint excerpt missing redacted text: %s", checkpoint.Excerpt)
	}
}

func TestIngestorKeepsCheckpointExcerptUntilCompletion(t *testing.T) {
	workingDir := t.TempDir()
	store := journal.NewStore(filepath.Join(t.TempDir(), ".threadmark"))
	ingestor := NewIngestor(store, core.Config{
		NewThreadID:    deterministicThreadIDs(),
		DebounceWindow: time.Minute,
	})

	if _, err := ingestor.Ingest(context.Background(), textEvent(t, workingDir, "evt_1", at(0), "/checkpoint")); err != nil {
		t.Fatalf("Ingest returned error: %v", err)
	}
	checkpoint, err := ingestor.FlushDue(context.Background(), workingDir, at(11*time.Minute))
	if err != nil {
		t.Fatalf("FlushDue returned error: %v", err)
	}
	if checkpoint == nil || checkpoint.Result.Checkpoint == nil {
		t.Fatalf("checkpoint = %#v, want checkpoint", checkpoint)
	}
	if checkpoint.Excerpt == "" {
		t.Fatal("checkpoint excerpt is empty")
	}
	if got := len(ingestor.buffers[checkpoint.ProjectHash]); got == 0 {
		t.Fatal("buffer was discarded before checkpoint completion")
	}

	ingestor.CompleteCheckpoint(*checkpoint)
	if got := len(ingestor.buffers[checkpoint.ProjectHash]); got != 0 {
		t.Fatalf("buffer length after completion = %d, want 0", got)
	}
}

func TestIngestorCheckIdleAllProducesCheckpoint(t *testing.T) {
	workingDir := t.TempDir()
	store := journal.NewStore(filepath.Join(t.TempDir(), ".threadmark"))
	ingestor := NewIngestor(store, core.Config{
		NewThreadID:      deterministicThreadIDs(),
		IdleTriggerAfter: time.Minute,
		DebounceWindow:   time.Minute,
	})

	if _, err := ingestor.Ingest(context.Background(), textEvent(t, workingDir, "evt_1", at(0), "start")); err != nil {
		t.Fatalf("Ingest returned error: %v", err)
	}
	results, err := ingestor.CheckIdleAll(context.Background(), at(2*time.Minute))
	if err != nil {
		t.Fatalf("CheckIdleAll returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Result.TriggerCandidate == nil || results[0].Result.TriggerCandidate.Type != core.TriggerIdle {
		t.Fatalf("trigger candidate = %#v, want idle", results[0].Result.TriggerCandidate)
	}

	results, err = ingestor.CheckIdleAll(context.Background(), at(3*time.Minute))
	if err != nil {
		t.Fatalf("CheckIdleAll returned error: %v", err)
	}
	if len(results) != 1 || results[0].Result.Checkpoint == nil {
		t.Fatalf("results = %#v, want checkpoint", results)
	}
	if results[0].Excerpt == "" {
		t.Fatal("checkpoint excerpt is empty")
	}
}

func textEvent(t *testing.T, workingDir, id string, ts time.Time, content string) core.Event {
	t.Helper()
	event, err := core.NewEvent(
		core.KindText,
		"codex",
		"transcript-1",
		workingDir,
		core.TextPayload{Actor: core.ActorUser, Content: content},
		core.WithEventID(id),
		core.WithTimestamp(ts),
	)
	if err != nil {
		t.Fatalf("NewEvent returned error: %v", err)
	}
	return event
}

func stopEvent(t *testing.T, workingDir, id string, ts time.Time) core.Event {
	t.Helper()
	event, err := core.NewEvent(
		core.KindText,
		"codex",
		"transcript-1",
		workingDir,
		core.TextPayload{Actor: core.ActorHarness, Content: "stop"},
		core.WithEventID(id),
		core.WithTimestamp(ts),
	)
	if err != nil {
		t.Fatalf("NewEvent returned error: %v", err)
	}
	return event
}

func deterministicThreadIDs() func(time.Time) (string, error) {
	return deterministicThreadIDsFrom(0)
}

func deterministicThreadIDsFrom(start int) func(time.Time) (string, error) {
	next := start
	return func(time.Time) (string, error) {
		next++
		return fmt.Sprintf("t_%d", next), nil
	}
}

func at(offset time.Duration) time.Time {
	return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC).Add(offset)
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
}
