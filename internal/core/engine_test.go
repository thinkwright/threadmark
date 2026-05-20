package core

import (
	"fmt"
	"testing"
	"time"
)

func TestEngineMapsTranscriptToInitialThread(t *testing.T) {
	engine := newTestEngine()
	event := textEvent(t, "evt_1", "transcript-1", at(0), ActorUser, "start")

	result, err := engine.Ingest(event)
	if err != nil {
		t.Fatalf("Ingest returned error: %v", err)
	}
	if result.ThreadBoundary == nil {
		t.Fatal("expected initial thread boundary")
	}
	if got, want := result.ThreadBoundary.Reason, ThreadBoundaryInitial; got != want {
		t.Fatalf("boundary reason = %s, want %s", got, want)
	}
	if got, want := result.ThreadID, "t_1"; got != want {
		t.Fatalf("thread id = %s, want %s", got, want)
	}
	if got := result.State.TranscriptToThread["transcript-1"]; got != "t_1" {
		t.Fatalf("transcript mapping = %s, want t_1", got)
	}
}

func TestEngineCreatesThreadBoundaryAfterTimeGap(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(textEvent(t, "evt_1", "transcript-1", at(0), ActorUser, "start")); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_2", "transcript-2", at(25*time.Hour), ActorUser, "resume"))
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if result.ThreadBoundary == nil {
		t.Fatal("expected time-gap thread boundary")
	}
	if got, want := result.ThreadBoundary.Reason, ThreadBoundaryTimeGap; got != want {
		t.Fatalf("boundary reason = %s, want %s", got, want)
	}
	if got, want := result.ThreadID, "t_2"; got != want {
		t.Fatalf("thread id = %s, want %s", got, want)
	}
}

func TestEngineCreatesThreadBoundaryOnWorkingDirChange(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(textEvent(t, "evt_1", "transcript-1", at(0), ActorUser, "start")); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	event, err := NewEvent(
		KindWorkingDirChange,
		"codex",
		"transcript-1",
		"/tmp/project",
		WorkingDirChangePayload{From: "/tmp/project", To: "/tmp/other"},
		WithEventID("evt_2"),
		WithTimestamp(at(time.Minute)),
	)
	if err != nil {
		t.Fatalf("NewEvent returned error: %v", err)
	}

	result, err := engine.Ingest(event)
	if err != nil {
		t.Fatalf("Ingest returned error: %v", err)
	}
	if result.ThreadBoundary == nil || result.ThreadBoundary.Reason != ThreadBoundaryWorkingDirChange {
		t.Fatalf("boundary = %#v, want working_dir_change", result.ThreadBoundary)
	}
	if result.TriggerCandidate == nil || result.TriggerCandidate.Type != TriggerBranchSwitch {
		t.Fatalf("trigger candidate = %#v, want branch-switch", result.TriggerCandidate)
	}
}

func TestEngineCreatesThreadBoundaryOnExplicitUserSignal(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(textEvent(t, "evt_1", "transcript-1", at(0), ActorUser, "start")); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_2", "transcript-1", at(time.Minute), ActorUser, "/checkpoint --new-thread"))
	if err != nil {
		t.Fatalf("Ingest returned error: %v", err)
	}
	if result.ThreadBoundary == nil || result.ThreadBoundary.Reason != ThreadBoundaryUserSignal {
		t.Fatalf("boundary = %#v, want user_signal", result.ThreadBoundary)
	}
	if result.TriggerCandidate == nil || result.TriggerCandidate.Type != TriggerUserCheckpoint {
		t.Fatalf("trigger candidate = %#v, want user-checkpoint", result.TriggerCandidate)
	}
}

func TestEngineCommitTriggerDebouncesUntilWindowExpires(t *testing.T) {
	engine := newTestEngine()
	event := fileChangeEvent(t, "evt_commit", "abc123", at(0))

	result, err := engine.Ingest(event)
	if err != nil {
		t.Fatalf("Ingest returned error: %v", err)
	}
	if result.TriggerDecision.Kind != TriggerDecisionPending {
		t.Fatalf("trigger decision = %s, want pending", result.TriggerDecision.Kind)
	}
	if checkpoint := engine.FlushDue(at(9 * time.Minute)); checkpoint != nil {
		t.Fatalf("checkpoint fired too early: %#v", checkpoint)
	}

	checkpoint := engine.FlushDue(at(10 * time.Minute))
	if checkpoint == nil {
		t.Fatal("expected checkpoint after debounce window")
	}
	if got, want := checkpoint.Trigger.Reason, "commit:abc123"; got != want {
		t.Fatalf("checkpoint trigger = %s, want %s", got, want)
	}
	if got, want := checkpoint.SourceRange.First, "evt_commit"; got != want {
		t.Fatalf("source first = %s, want %s", got, want)
	}
}

func TestEngineFlushesDuePendingTriggerOnLaterOrdinaryEvent(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(fileChangeEvent(t, "evt_commit", "abc123", at(0))); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_later", "transcript-1", at(11*time.Minute), ActorAssistant, "done"))
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if result.Checkpoint == nil {
		t.Fatal("expected due pending trigger to fire on later event")
	}
	if got, want := result.Checkpoint.Trigger.Reason, "commit:abc123"; got != want {
		t.Fatalf("checkpoint trigger = %s, want %s", got, want)
	}
}

func TestEngineHigherPriorityTriggerReplacesPendingTrigger(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(fileChangeEvent(t, "evt_commit", "abc123", at(0))); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_checkpoint", "transcript-1", at(time.Minute), ActorUser, "/checkpoint"))
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if got, want := result.TriggerDecision.Kind, TriggerDecisionReplaced; got != want {
		t.Fatalf("trigger decision = %s, want %s", got, want)
	}
	if result.TriggerDecision.Replaced == nil || result.TriggerDecision.Replaced.Type != TriggerCommit {
		t.Fatalf("replaced trigger = %#v, want commit", result.TriggerDecision.Replaced)
	}

	checkpoint := engine.FlushDue(at(11 * time.Minute))
	if checkpoint == nil {
		t.Fatal("expected checkpoint")
	}
	if got, want := checkpoint.Trigger.Type, TriggerUserCheckpoint; got != want {
		t.Fatalf("checkpoint type = %s, want %s", got, want)
	}
	if got, want := checkpoint.SourceRange.Last, "evt_checkpoint"; got != want {
		t.Fatalf("source last = %s, want %s", got, want)
	}
}

func TestEngineLowerPriorityTriggerIsSkippedDuringDebounceWindow(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(textEvent(t, "evt_checkpoint", "transcript-1", at(0), ActorUser, "/checkpoint")); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	result, err := engine.Ingest(fileChangeEvent(t, "evt_commit", "abc123", at(time.Minute)))
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if got, want := result.TriggerDecision.Kind, TriggerDecisionSkipped; got != want {
		t.Fatalf("trigger decision = %s, want %s", got, want)
	}
}

func TestEngineSafetyNetTrigger(t *testing.T) {
	engine := NewEngine(ProjectState{}, Config{
		SafetyNetTurns: 3,
		NewThreadID:    deterministicThreadIDs(),
	})

	var result IngestResult
	for i := 1; i <= 3; i++ {
		event := textEvent(t, fmt.Sprintf("evt_%d", i), "transcript-1", at(time.Duration(i)*time.Minute), ActorUser, "turn")
		var err error
		result, err = engine.Ingest(event)
		if err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}
	if result.TriggerCandidate == nil || result.TriggerCandidate.Type != TriggerSafetyNet {
		t.Fatalf("trigger candidate = %#v, want safety-net", result.TriggerCandidate)
	}
}

func TestEngineIdleDoesNotRepeatWithoutNewActivity(t *testing.T) {
	engine := NewEngine(ProjectState{}, Config{
		NewThreadID:      deterministicThreadIDs(),
		IdleTriggerAfter: time.Minute,
		DebounceWindow:   time.Minute,
	})
	if _, err := engine.Ingest(textEvent(t, "evt_1", "transcript-1", at(0), ActorUser, "start")); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if decision, checkpoint := engine.CheckIdle(at(2 * time.Minute)); decision == nil || checkpoint != nil {
		t.Fatalf("first idle check decision=%#v checkpoint=%#v, want pending decision", decision, checkpoint)
	}
	if decision, checkpoint := engine.CheckIdle(at(3 * time.Minute)); decision != nil || checkpoint == nil {
		t.Fatalf("second idle check decision=%#v checkpoint=%#v, want checkpoint", decision, checkpoint)
	} else {
		if got, want := checkpoint.TranscriptID, "transcript-1"; got != want {
			t.Fatalf("idle checkpoint transcript_id = %q, want %q", got, want)
		}
		if got, want := checkpoint.Harness, "codex"; got != want {
			t.Fatalf("idle checkpoint harness = %q, want %q", got, want)
		}
	}
	if decision, checkpoint := engine.CheckIdle(at(4 * time.Minute)); decision != nil || checkpoint != nil {
		t.Fatalf("third idle check decision=%#v checkpoint=%#v, want no repeat", decision, checkpoint)
	}
}

func TestEngineStopDoesNotTriggerAfterOnlyLifecycleEvents(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(textEvent(t, "evt_start", "transcript-1", at(0), ActorHarness, "session_start source=startup")); err != nil {
		t.Fatalf("session start ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_stop", "transcript-1", at(time.Minute), ActorHarness, "stop"))
	if err != nil {
		t.Fatalf("stop ingest: %v", err)
	}
	if result.TriggerCandidate != nil {
		t.Fatalf("trigger candidate = %#v, want nil for empty lifecycle window", result.TriggerCandidate)
	}
	if result.TriggerDecision.Kind != TriggerDecisionNone {
		t.Fatalf("trigger decision = %s, want none", result.TriggerDecision.Kind)
	}
	if result.Checkpoint != nil {
		t.Fatalf("checkpoint = %#v, want nil", result.Checkpoint)
	}
	if got := result.State.CurrentRangeStartID; got != "" {
		t.Fatalf("current range start = %q, want empty", got)
	}
}

func TestEngineIgnoresDuplicateEventID(t *testing.T) {
	engine := newTestEngine()
	event := textEvent(t, "evt_1", "transcript-1", at(0), ActorUser, "start")

	first, err := engine.Ingest(event)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if first.Duplicate {
		t.Fatal("first ingest was marked duplicate")
	}
	if got, want := first.State.TurnsSinceCheckpoint, 1; got != want {
		t.Fatalf("turns after first event = %d, want %d", got, want)
	}

	duplicate, err := engine.Ingest(event)
	if err != nil {
		t.Fatalf("duplicate ingest: %v", err)
	}
	if !duplicate.Duplicate {
		t.Fatal("duplicate ingest was not marked duplicate")
	}
	if duplicate.ThreadBoundary != nil {
		t.Fatalf("duplicate thread boundary = %#v, want nil", duplicate.ThreadBoundary)
	}
	if duplicate.TriggerCandidate != nil {
		t.Fatalf("duplicate trigger candidate = %#v, want nil", duplicate.TriggerCandidate)
	}
	if duplicate.Checkpoint != nil {
		t.Fatalf("duplicate checkpoint = %#v, want nil", duplicate.Checkpoint)
	}
	if got, want := duplicate.State.TurnsSinceCheckpoint, first.State.TurnsSinceCheckpoint; got != want {
		t.Fatalf("turns after duplicate = %d, want %d", got, want)
	}
	if got, want := duplicate.State.SubstantiveEvents, first.State.SubstantiveEvents; got != want {
		t.Fatalf("substantive events after duplicate = %d, want %d", got, want)
	}
}

func TestEngineStopTriggersAfterSubstantiveUserEvent(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(textEvent(t, "evt_start", "transcript-1", at(0), ActorHarness, "session_start source=startup")); err != nil {
		t.Fatalf("session start ingest: %v", err)
	}
	if _, err := engine.Ingest(textEvent(t, "evt_user", "transcript-1", at(time.Minute), ActorUser, "do the work")); err != nil {
		t.Fatalf("user ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_stop", "transcript-1", at(2*time.Minute), ActorHarness, "stop"))
	if err != nil {
		t.Fatalf("stop ingest: %v", err)
	}
	if result.TriggerCandidate == nil || result.TriggerCandidate.Type != TriggerStop {
		t.Fatalf("trigger candidate = %#v, want stop", result.TriggerCandidate)
	}
	if !result.TriggerCandidate.Immediate {
		t.Fatalf("trigger candidate immediate = false, want true")
	}
	if result.TriggerDecision.Kind != TriggerDecisionFired {
		t.Fatalf("trigger decision = %s, want fired", result.TriggerDecision.Kind)
	}

	checkpoint := result.Checkpoint
	if checkpoint == nil {
		t.Fatal("expected immediate stop checkpoint")
	}
	if got, want := checkpoint.SourceRange.First, "evt_user"; got != want {
		t.Fatalf("source range first = %q, want %q", got, want)
	}
	if got, want := checkpoint.SourceRange.Last, "evt_stop"; got != want {
		t.Fatalf("source range last = %q, want %q", got, want)
	}
}

func TestEngineCheckpointAggregatesSourceMetadata(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(fileChangeEvent(t, "evt_file", "", at(0))); err != nil {
		t.Fatalf("file ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_stop", "transcript-1", at(time.Minute), ActorHarness, "stop"))
	if err != nil {
		t.Fatalf("stop ingest: %v", err)
	}
	if result.TriggerCandidate == nil || result.TriggerCandidate.Type != TriggerStop {
		t.Fatalf("trigger candidate = %#v, want stop", result.TriggerCandidate)
	}

	checkpoint := result.Checkpoint
	if checkpoint == nil {
		t.Fatal("expected immediate stop checkpoint")
	}
	if got, want := checkpoint.FilesTouched, []string{"main.go"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("files touched = %#v, want %#v", got, want)
	}
}

func TestEngineIdleDoesNotTriggerAfterOnlyLifecycleEvents(t *testing.T) {
	engine := NewEngine(ProjectState{}, Config{
		NewThreadID:      deterministicThreadIDs(),
		IdleTriggerAfter: time.Minute,
		DebounceWindow:   time.Minute,
	})
	if _, err := engine.Ingest(textEvent(t, "evt_start", "transcript-1", at(0), ActorHarness, "session_start source=startup")); err != nil {
		t.Fatalf("session start ingest: %v", err)
	}
	if decision, checkpoint := engine.CheckIdle(at(2 * time.Minute)); decision != nil || checkpoint != nil {
		t.Fatalf("idle check decision=%#v checkpoint=%#v, want no lifecycle-only idle trigger", decision, checkpoint)
	}
}

func TestEnginePreCompactFiresImmediatelyWithoutDebounce(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(textEvent(t, "evt_user", "transcript-1", at(0), ActorUser, "start")); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_pre_compact", "transcript-1", at(time.Minute), ActorHarness, "pre_compact cause=manual"))
	if err != nil {
		t.Fatalf("pre-compact ingest: %v", err)
	}
	if result.TriggerCandidate == nil || result.TriggerCandidate.Type != TriggerPreCompact {
		t.Fatalf("trigger candidate = %#v, want pre-compact", result.TriggerCandidate)
	}
	if !result.TriggerCandidate.Immediate {
		t.Fatal("expected pre-compact candidate to be Immediate")
	}
	if got, want := result.TriggerCandidate.Reason, "pre-compact:manual"; got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
	if got, want := result.TriggerCandidate.Priority, 88; got != want {
		t.Fatalf("priority = %d, want %d", got, want)
	}
	if got, want := result.TriggerDecision.Kind, TriggerDecisionFired; got != want {
		t.Fatalf("decision kind = %s, want fired", got)
	}
	if result.Checkpoint == nil {
		t.Fatal("expected immediate checkpoint")
	}
	if got, want := result.Checkpoint.Trigger.Reason, "pre-compact:manual"; got != want {
		t.Fatalf("checkpoint trigger reason = %q, want %q", got, want)
	}
	if got, want := result.Checkpoint.SourceRange.Last, "evt_pre_compact"; got != want {
		t.Fatalf("source range last = %q, want %q", got, want)
	}
}

func TestEnginePreCompactDefaultsToAutoCause(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(textEvent(t, "evt_user", "transcript-1", at(0), ActorUser, "start")); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_pc", "transcript-1", at(time.Minute), ActorHarness, "pre_compact"))
	if err != nil {
		t.Fatalf("pre-compact ingest: %v", err)
	}
	if result.TriggerCandidate == nil || result.TriggerCandidate.Reason != "pre-compact:auto" {
		t.Fatalf("trigger reason = %#v, want pre-compact:auto", result.TriggerCandidate)
	}
	if result.Checkpoint == nil {
		t.Fatal("expected immediate checkpoint even with default cause")
	}
}

func TestEnginePreCompactAbsorbsPendingCommit(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(fileChangeEvent(t, "evt_commit", "abc123", at(0))); err != nil {
		t.Fatalf("commit ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_pc", "transcript-1", at(2*time.Minute), ActorHarness, "pre_compact cause=auto"))
	if err != nil {
		t.Fatalf("pre-compact ingest: %v", err)
	}
	if got, want := result.TriggerDecision.Kind, TriggerDecisionFired; got != want {
		t.Fatalf("decision kind = %s, want fired", got)
	}
	if result.TriggerDecision.Replaced == nil || result.TriggerDecision.Replaced.Type != TriggerCommit {
		t.Fatalf("replaced = %#v, want absorbed commit", result.TriggerDecision.Replaced)
	}
	if result.Checkpoint == nil {
		t.Fatal("expected checkpoint")
	}
	if got, want := result.Checkpoint.Trigger.Type, TriggerPreCompact; got != want {
		t.Fatalf("checkpoint trigger type = %s, want pre-compact", got)
	}
	// The pre-compact source range covers from the commit (the previous
	// pending) through the pre-compact event itself — events that the
	// commit's would-be summary would have included are absorbed.
	if got, want := result.Checkpoint.SourceRange.First, "evt_commit"; got != want {
		t.Fatalf("source range first = %q, want %q", got, want)
	}
	if got, want := result.Checkpoint.SourceRange.Last, "evt_pc"; got != want {
		t.Fatalf("source range last = %q, want %q", got, want)
	}
}

func TestEngineStopAbsorbsPendingCommit(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(fileChangeEvent(t, "evt_commit", "abc123", at(0))); err != nil {
		t.Fatalf("commit ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_stop", "transcript-1", at(2*time.Minute), ActorHarness, "stop"))
	if err != nil {
		t.Fatalf("stop ingest: %v", err)
	}
	if got, want := result.TriggerDecision.Kind, TriggerDecisionFired; got != want {
		t.Fatalf("decision kind = %s, want fired", got)
	}
	if result.TriggerDecision.Replaced == nil || result.TriggerDecision.Replaced.Type != TriggerCommit {
		t.Fatalf("replaced = %#v, want absorbed commit", result.TriggerDecision.Replaced)
	}
	if result.Checkpoint == nil {
		t.Fatal("expected checkpoint")
	}
	if got, want := result.Checkpoint.Trigger.Type, TriggerStop; got != want {
		t.Fatalf("checkpoint trigger type = %s, want stop", got)
	}
	if got, want := result.Checkpoint.SourceRange.First, "evt_commit"; got != want {
		t.Fatalf("source range first = %q, want %q", got, want)
	}
	if got, want := result.Checkpoint.SourceRange.Last, "evt_stop"; got != want {
		t.Fatalf("source range last = %q, want %q", got, want)
	}
}

func TestEnginePreCompactAbsorbsOverduePendingCommit(t *testing.T) {
	engine := newTestEngine()
	if _, err := engine.Ingest(fileChangeEvent(t, "evt_commit", "abc123", at(0))); err != nil {
		t.Fatalf("commit ingest: %v", err)
	}

	result, err := engine.Ingest(textEvent(t, "evt_pc", "transcript-1", at(11*time.Minute), ActorHarness, "pre_compact cause=auto"))
	if err != nil {
		t.Fatalf("pre-compact ingest: %v", err)
	}
	if got, want := result.TriggerDecision.Kind, TriggerDecisionFired; got != want {
		t.Fatalf("decision kind = %s, want fired", got)
	}
	if result.TriggerDecision.Replaced == nil || result.TriggerDecision.Replaced.Type != TriggerCommit {
		t.Fatalf("replaced = %#v, want absorbed overdue commit", result.TriggerDecision.Replaced)
	}
	if result.Checkpoint == nil {
		t.Fatal("expected checkpoint")
	}
	if got, want := result.Checkpoint.Trigger.Type, TriggerPreCompact; got != want {
		t.Fatalf("checkpoint trigger type = %s, want pre-compact", got)
	}
	if got, want := result.Checkpoint.SourceRange.First, "evt_commit"; got != want {
		t.Fatalf("source range first = %q, want %q", got, want)
	}
	if got, want := result.Checkpoint.SourceRange.Last, "evt_pc"; got != want {
		t.Fatalf("source range last = %q, want %q", got, want)
	}
	if checkpoint := engine.FlushDue(at(12 * time.Minute)); checkpoint != nil {
		t.Fatalf("unexpected remaining due checkpoint: %#v", checkpoint)
	}
}

func newTestEngine() *Engine {
	return NewEngine(ProjectState{}, Config{NewThreadID: deterministicThreadIDs()})
}

func deterministicThreadIDs() func(time.Time) (string, error) {
	var next int
	return func(time.Time) (string, error) {
		next++
		return fmt.Sprintf("t_%d", next), nil
	}
}

func textEvent(t *testing.T, id, transcriptID string, ts time.Time, actor TextActor, content string) Event {
	t.Helper()
	event, err := NewEvent(
		KindText,
		"codex",
		transcriptID,
		"/tmp/project",
		TextPayload{Actor: actor, Content: content},
		WithEventID(id),
		WithTimestamp(ts),
	)
	if err != nil {
		t.Fatalf("NewEvent returned error: %v", err)
	}
	return event
}

func fileChangeEvent(t *testing.T, id, commit string, ts time.Time) Event {
	t.Helper()
	event, err := NewEvent(
		KindFileChange,
		"codex",
		"transcript-1",
		"/tmp/project",
		FileChangePayload{Path: "main.go", Op: FileOpModify, GitCommit: commit},
		WithEventID(id),
		WithTimestamp(ts),
	)
	if err != nil {
		t.Fatalf("NewEvent returned error: %v", err)
	}
	return event
}

func at(offset time.Duration) time.Time {
	return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC).Add(offset)
}
