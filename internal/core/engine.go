package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	DefaultThreadBoundaryAfter = 24 * time.Hour
	DefaultIdleTriggerAfter    = 30 * time.Minute
	DefaultDebounceWindow      = 10 * time.Minute
	DefaultSafetyNetTurns      = 50
	recentEventIDLimit         = 128
)

type ThreadBoundaryReason string

const (
	ThreadBoundaryInitial          ThreadBoundaryReason = "initial"
	ThreadBoundaryTimeGap          ThreadBoundaryReason = "time_gap"
	ThreadBoundaryWorkingDirChange ThreadBoundaryReason = "working_dir_change"
	ThreadBoundaryUserSignal       ThreadBoundaryReason = "user_signal"
)

type TriggerType string

const (
	TriggerIdle           TriggerType = "idle"
	TriggerCommit         TriggerType = "commit"
	TriggerBranchSwitch   TriggerType = "branch-switch"
	TriggerStop           TriggerType = "stop"
	TriggerUserCheckpoint TriggerType = "user-checkpoint"
	TriggerSafetyNet      TriggerType = "safety-net"
	TriggerPreCompact     TriggerType = "pre-compact"
)

type TriggerDecisionKind string

const (
	TriggerDecisionNone     TriggerDecisionKind = ""
	TriggerDecisionPending  TriggerDecisionKind = "pending"
	TriggerDecisionReplaced TriggerDecisionKind = "replaced"
	TriggerDecisionSkipped  TriggerDecisionKind = "skipped"
	TriggerDecisionFired    TriggerDecisionKind = "fired"
)

type Config struct {
	ThreadBoundaryAfter time.Duration
	IdleTriggerAfter    time.Duration
	DebounceWindow      time.Duration
	SafetyNetTurns      int
	NewThreadID         func(time.Time) (string, error)
}

func (c Config) withDefaults() Config {
	if c.ThreadBoundaryAfter <= 0 {
		c.ThreadBoundaryAfter = DefaultThreadBoundaryAfter
	}
	if c.IdleTriggerAfter <= 0 {
		c.IdleTriggerAfter = DefaultIdleTriggerAfter
	}
	if c.DebounceWindow <= 0 {
		c.DebounceWindow = DefaultDebounceWindow
	}
	if c.SafetyNetTurns <= 0 {
		c.SafetyNetTurns = DefaultSafetyNetTurns
	}
	if c.NewThreadID == nil {
		c.NewThreadID = NewThreadID
	}
	return c
}

type ProjectState struct {
	SchemaVersion        int               `json:"schema_version"`
	ProjectHash          string            `json:"project_hash,omitempty"`
	WorkingDir           string            `json:"working_dir"`
	CurrentThreadID      string            `json:"current_thread_id"`
	TranscriptToThread   map[string]string `json:"transcript_to_thread"`
	LastTranscriptID     string            `json:"last_transcript_id,omitempty"`
	LastHarness          string            `json:"last_harness,omitempty"`
	LastActivityTS       time.Time         `json:"last_activity_ts,omitempty"`
	LastCheckpointTS     time.Time         `json:"last_checkpoint_ts,omitempty"`
	LastProcessedEventID string            `json:"last_processed_event_id,omitempty"`
	RecentEventIDs       []string          `json:"recent_event_ids,omitempty"`
	CurrentRangeStartID  string            `json:"current_range_start_event_id,omitempty"`
	TurnsSinceCheckpoint int               `json:"turns_since_checkpoint,omitempty"`
	SubstantiveEvents    int               `json:"substantive_events_since_checkpoint,omitempty"`
	FilesTouched         []string          `json:"files_touched_since_checkpoint,omitempty"`
	CommitRefs           []string          `json:"commit_refs_since_checkpoint,omitempty"`
	Debounce             DebounceState     `json:"debounce,omitempty"`
}

func (s ProjectState) Normalize() ProjectState {
	if s.SchemaVersion == 0 {
		s.SchemaVersion = CurrentSchemaVersion
	}
	if s.TranscriptToThread == nil {
		s.TranscriptToThread = make(map[string]string)
	}
	s.LastActivityTS = normalizeTime(s.LastActivityTS)
	s.LastCheckpointTS = normalizeTime(s.LastCheckpointTS)
	s.RecentEventIDs = limitRecentEventIDs(uniqueStrings(s.RecentEventIDs))
	s.FilesTouched = uniqueStrings(s.FilesTouched)
	s.CommitRefs = uniqueStrings(s.CommitRefs)
	if s.SubstantiveEvents == 0 && s.TurnsSinceCheckpoint > 0 {
		s.SubstantiveEvents = s.TurnsSinceCheckpoint
	}
	s.Debounce = s.Debounce.Normalize()
	return s
}

func (s ProjectState) Clone() ProjectState {
	s = s.Normalize()
	mapping := make(map[string]string, len(s.TranscriptToThread))
	for k, v := range s.TranscriptToThread {
		mapping[k] = v
	}
	s.TranscriptToThread = mapping
	s.RecentEventIDs = append([]string(nil), s.RecentEventIDs...)
	s.FilesTouched = append([]string(nil), s.FilesTouched...)
	s.CommitRefs = append([]string(nil), s.CommitRefs...)
	return s
}

type DebounceState struct {
	WindowStartedTS               time.Time         `json:"window_started_ts,omitempty"`
	HighestPriorityTrigger        string            `json:"highest_priority_trigger_in_window,omitempty"`
	HighestPriority               int               `json:"highest_priority,omitempty"`
	PendingTrigger                *TriggerCandidate `json:"pending_trigger,omitempty"`
	PendingSourceEventRange       []string          `json:"pending_source_event_range,omitempty"`
	PendingSourceStartedAt        time.Time         `json:"pending_source_started_at,omitempty"`
	PendingSourceEndedAt          time.Time         `json:"pending_source_ended_at,omitempty"`
	PendingTriggerTranscriptID    string            `json:"pending_trigger_transcript_id,omitempty"`
	PendingTriggerHarness         string            `json:"pending_trigger_harness,omitempty"`
	PendingTriggerWorkingDir      string            `json:"pending_trigger_working_dir,omitempty"`
	PendingTriggerThreadID        string            `json:"pending_trigger_thread_id,omitempty"`
	PendingTriggerFilesTouched    []string          `json:"pending_trigger_files_touched,omitempty"`
	PendingTriggerCommitRefs      []string          `json:"pending_trigger_commit_refs,omitempty"`
	PendingTriggerSourceTruncated bool              `json:"pending_trigger_source_truncated,omitempty"`
}

func (d DebounceState) Normalize() DebounceState {
	d.WindowStartedTS = normalizeTime(d.WindowStartedTS)
	d.PendingSourceStartedAt = normalizeTime(d.PendingSourceStartedAt)
	d.PendingSourceEndedAt = normalizeTime(d.PendingSourceEndedAt)
	if d.PendingTrigger != nil {
		normalized := d.PendingTrigger.Normalize()
		d.PendingTrigger = &normalized
	}
	return d
}

type TriggerCandidate struct {
	Type      TriggerType `json:"type"`
	Reason    string      `json:"reason"`
	Priority  int         `json:"priority"`
	Immediate bool        `json:"immediate,omitempty"`
	EventID   string      `json:"event_id,omitempty"`
	At        time.Time   `json:"at"`
}

func (c TriggerCandidate) Normalize() TriggerCandidate {
	c.At = normalizeTime(c.At)
	if c.Reason == "" {
		c.Reason = string(c.Type)
	}
	return c
}

type ThreadBoundary struct {
	ThreadID string               `json:"thread_id"`
	Reason   ThreadBoundaryReason `json:"reason"`
	At       time.Time            `json:"at"`
}

type TriggerDecision struct {
	Kind        TriggerDecisionKind `json:"kind"`
	Candidate   *TriggerCandidate   `json:"candidate,omitempty"`
	Skipped     *TriggerCandidate   `json:"skipped,omitempty"`
	Replaced    *TriggerCandidate   `json:"replaced,omitempty"`
	Description string              `json:"description,omitempty"`
}

type SourceEventRange struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

type CheckpointRequest struct {
	Trigger         TriggerCandidate `json:"trigger"`
	ThreadID        string           `json:"thread_id"`
	TranscriptID    string           `json:"transcript_id"`
	Harness         string           `json:"harness"`
	WorkingDir      string           `json:"working_dir"`
	SourceRange     SourceEventRange `json:"source_event_range"`
	SourceStartedAt time.Time        `json:"source_started_at"`
	SourceEndedAt   time.Time        `json:"source_ended_at"`
	FilesTouched    []string         `json:"files_touched,omitempty"`
	CommitRefs      []string         `json:"commit_refs,omitempty"`
	SourceTruncated bool             `json:"source_truncated,omitempty"`
}

type IngestResult struct {
	EventID          string             `json:"event_id"`
	EventKind        EventKind          `json:"event_kind"`
	Harness          string             `json:"harness"`
	TranscriptID     string             `json:"transcript_id"`
	WorkingDir       string             `json:"working_dir"`
	SourceSeq        int64              `json:"source_seq,omitempty"`
	ThreadID         string             `json:"thread_id"`
	ThreadBoundary   *ThreadBoundary    `json:"thread_boundary,omitempty"`
	TriggerCandidate *TriggerCandidate  `json:"trigger_candidate,omitempty"`
	TriggerDecision  TriggerDecision    `json:"trigger_decision,omitempty"`
	Checkpoint       *CheckpointRequest `json:"checkpoint,omitempty"`
	Duplicate        bool               `json:"duplicate,omitempty"`
	State            ProjectState       `json:"state"`
}

type Engine struct {
	config Config
	state  ProjectState
}

func NewEngine(state ProjectState, config Config) *Engine {
	return &Engine{
		config: config.withDefaults(),
		state:  state.Normalize(),
	}
}

func (e *Engine) State() ProjectState {
	return e.state.Clone()
}

func (e *Engine) Ingest(event Event) (IngestResult, error) {
	event = event.Normalize()
	if err := event.Validate(); err != nil {
		return IngestResult{}, err
	}

	e.state = e.state.Normalize()
	if e.state.WorkingDir == "" {
		e.state.WorkingDir = event.WorkingDir
	}
	result := IngestResult{
		EventID:      event.EventID,
		EventKind:    event.Kind,
		Harness:      event.Harness,
		TranscriptID: event.TranscriptID,
		WorkingDir:   event.WorkingDir,
		SourceSeq:    event.SourceSeq,
	}
	if e.hasProcessedEvent(event.EventID) {
		result.ThreadID = e.state.CurrentThreadID
		result.Duplicate = true
		result.State = e.State()
		return result, nil
	}

	boundary, err := e.applyThreadBoundary(event)
	if err != nil {
		return IngestResult{}, err
	}
	if boundary != nil {
		result.ThreadBoundary = boundary
	}
	threadID := e.state.CurrentThreadID
	e.state.TranscriptToThread[event.TranscriptID] = threadID
	result.ThreadID = threadID

	candidate, err := e.classifyImmediateTrigger(event)
	if err != nil {
		return IngestResult{}, err
	}
	if candidate != nil {
		normalized := candidate.Normalize()
		result.TriggerCandidate = &normalized
		decision, checkpoint := e.applyCandidate(normalized, event)
		result.TriggerDecision = decision
		if checkpoint != nil {
			result.Checkpoint = checkpoint
		}
		e.state.LastActivityTS = event.Ts
		e.state.LastTranscriptID = event.TranscriptID
		e.state.LastHarness = event.Harness
		e.markProcessedEvent(event.EventID)
		result.State = e.State()
		return result, nil
	}

	if checkpoint := e.FlushDue(event.Ts); checkpoint != nil {
		result.Checkpoint = checkpoint
	}

	if isSubstantiveEvent(event) {
		if e.state.CurrentRangeStartID == "" {
			e.state.CurrentRangeStartID = event.EventID
		}
		e.state.SubstantiveEvents++
		e.recordSourceMetadata(event)
	}
	if isTurnEvent(event) {
		e.state.TurnsSinceCheckpoint++
	}

	candidate, err = e.classifyTrigger(event)
	if err != nil {
		return IngestResult{}, err
	}
	if candidate != nil {
		normalized := candidate.Normalize()
		result.TriggerCandidate = &normalized
		decision, checkpoint := e.applyCandidate(normalized, event)
		result.TriggerDecision = decision
		if checkpoint != nil {
			result.Checkpoint = checkpoint
		}
	}

	e.state.LastActivityTS = event.Ts
	e.state.LastTranscriptID = event.TranscriptID
	e.state.LastHarness = event.Harness
	e.markProcessedEvent(event.EventID)
	result.State = e.State()
	return result, nil
}

func (e *Engine) FlushDue(now time.Time) *CheckpointRequest {
	now = normalizeTime(now)
	if now.IsZero() || !e.pendingDue(now) {
		return nil
	}
	return e.firePending(now)
}

func (e *Engine) applyThreadBoundary(event Event) (*ThreadBoundary, error) {
	reason := ThreadBoundaryReason("")
	switch {
	case e.state.CurrentThreadID == "":
		reason = ThreadBoundaryInitial
	case !e.state.LastActivityTS.IsZero() && event.Ts.Sub(e.state.LastActivityTS) > e.config.ThreadBoundaryAfter:
		reason = ThreadBoundaryTimeGap
	case event.Kind == KindWorkingDirChange:
		reason = ThreadBoundaryWorkingDirChange
	case hasNewThreadSignal(event):
		reason = ThreadBoundaryUserSignal
	}

	if reason == "" {
		return nil, nil
	}

	threadID, err := e.config.NewThreadID(event.Ts)
	if err != nil {
		return nil, err
	}
	e.state.CurrentThreadID = threadID
	e.state.CurrentRangeStartID = ""
	e.state.TurnsSinceCheckpoint = 0
	e.state.SubstantiveEvents = 0
	e.state.FilesTouched = nil
	e.state.CommitRefs = nil
	return &ThreadBoundary{ThreadID: threadID, Reason: reason, At: event.Ts}, nil
}

func (e *Engine) classifyImmediateTrigger(event Event) (*TriggerCandidate, error) {
	if cause, ok := preCompactCause(event); ok {
		if !e.hasSubstantiveRange() {
			return nil, nil
		}
		return &TriggerCandidate{
			Type:      TriggerPreCompact,
			Reason:    "pre-compact:" + cause,
			Priority:  88,
			Immediate: true,
			EventID:   event.EventID,
			At:        event.Ts,
		}, nil
	}
	if isStopEvent(event) && e.hasSubstantiveRange() {
		return &TriggerCandidate{
			Type:      TriggerStop,
			Reason:    string(TriggerStop),
			Priority:  85,
			Immediate: true,
			EventID:   event.EventID,
			At:        event.Ts,
		}, nil
	}
	return nil, nil
}

func (e *Engine) classifyTrigger(event Event) (*TriggerCandidate, error) {
	if hasUserCheckpointSignal(event) {
		return &TriggerCandidate{
			Type:     TriggerUserCheckpoint,
			Reason:   string(TriggerUserCheckpoint),
			Priority: 90,
			EventID:  event.EventID,
			At:       event.Ts,
		}, nil
	}

	switch event.Kind {
	case KindFileChange:
		payload, err := DecodePayload[FileChangePayload](event.Payload)
		if err != nil {
			return nil, err
		}
		if payload.GitCommit != "" {
			return &TriggerCandidate{
				Type:     TriggerCommit,
				Reason:   "commit:" + payload.GitCommit,
				Priority: 80,
				EventID:  event.EventID,
				At:       event.Ts,
			}, nil
		}
	case KindWorkingDirChange:
		return &TriggerCandidate{
			Type:     TriggerBranchSwitch,
			Reason:   string(TriggerBranchSwitch),
			Priority: 70,
			EventID:  event.EventID,
			At:       event.Ts,
		}, nil
	}

	if e.state.TurnsSinceCheckpoint >= e.config.SafetyNetTurns {
		return &TriggerCandidate{
			Type:     TriggerSafetyNet,
			Reason:   fmt.Sprintf("%s:%d", TriggerSafetyNet, e.state.TurnsSinceCheckpoint),
			Priority: 40,
			EventID:  event.EventID,
			At:       event.Ts,
		}, nil
	}

	return nil, nil
}

func (e *Engine) CheckIdle(now time.Time) (*TriggerDecision, *CheckpointRequest) {
	now = normalizeTime(now)
	if now.IsZero() || e.state.LastActivityTS.IsZero() {
		return nil, nil
	}
	if !e.state.LastCheckpointTS.IsZero() && !e.state.LastCheckpointTS.Before(e.state.LastActivityTS) {
		return nil, e.FlushDue(now)
	}
	if now.Sub(e.state.LastActivityTS) < e.config.IdleTriggerAfter {
		return nil, e.FlushDue(now)
	}

	checkpoint := e.FlushDue(now)
	if checkpoint != nil {
		return nil, checkpoint
	}
	if !e.hasSubstantiveRange() {
		return nil, nil
	}

	candidate := TriggerCandidate{
		Type:     TriggerIdle,
		Reason:   "idle:" + e.config.IdleTriggerAfter.String(),
		Priority: 30,
		EventID:  e.state.LastProcessedEventID,
		At:       now,
	}
	decision, checkpoint := e.applyCandidate(candidate, Event{
		EventID:      e.state.LastProcessedEventID,
		Ts:           now,
		Harness:      e.state.LastHarness,
		TranscriptID: e.state.LastTranscriptID,
		WorkingDir:   e.state.WorkingDir,
	})
	return &decision, checkpoint
}

func (e *Engine) applyCandidate(candidate TriggerCandidate, event Event) (TriggerDecision, *CheckpointRequest) {
	candidate = candidate.Normalize()

	if candidate.Immediate {
		// Immediate triggers (e.g., pre-compact) bypass the debounce window:
		// the harness is about to rewrite or summarize the working context,
		// so we capture from CurrentRangeStartID through this event right
		// now. Any pending trigger in the window is absorbed; its events
		// are already included in the current source range.
		var replacedCopy *TriggerCandidate
		if existing := e.state.Debounce.PendingTrigger; existing != nil {
			c := *existing
			replacedCopy = &c
		}
		e.setPending(candidate, event)
		checkpoint := e.firePending(candidate.At)
		description := "immediate_fire: " + candidate.Reason
		if replacedCopy != nil {
			description = "immediate_fire: " + candidate.Reason + "; absorbed pending " + replacedCopy.Reason
		}
		return TriggerDecision{
			Kind:        TriggerDecisionFired,
			Candidate:   &candidate,
			Replaced:    replacedCopy,
			Description: description,
		}, checkpoint
	}

	if e.state.Debounce.PendingTrigger == nil {
		e.setPending(candidate, event)
		return TriggerDecision{Kind: TriggerDecisionPending, Candidate: &candidate}, nil
	}

	if e.pendingDue(candidate.At) {
		checkpoint := e.firePending(candidate.At)
		e.setPending(candidate, event)
		return TriggerDecision{
			Kind:        TriggerDecisionPending,
			Candidate:   &candidate,
			Description: "previous pending trigger became checkpoint: " + checkpoint.Trigger.Reason,
		}, checkpoint
	}

	pending := *e.state.Debounce.PendingTrigger
	if candidate.Priority > pending.Priority || (candidate.Priority == pending.Priority && candidate.At.After(pending.At)) {
		e.setPending(candidate, event)
		return TriggerDecision{
			Kind:      TriggerDecisionReplaced,
			Candidate: &candidate,
			Replaced:  &pending,
		}, nil
	}

	return TriggerDecision{
		Kind:      TriggerDecisionSkipped,
		Candidate: &candidate,
		Skipped:   &candidate,
		Description: fmt.Sprintf(
			"debounce_lower_priority: pending %q priority %d beats candidate priority %d",
			pending.Reason,
			pending.Priority,
			candidate.Priority,
		),
	}, nil
}

func (e *Engine) setPending(candidate TriggerCandidate, event Event) {
	first := e.state.CurrentRangeStartID
	if first == "" {
		first = event.EventID
	}
	last := candidate.EventID
	if last == "" {
		last = event.EventID
	}

	e.state.Debounce = DebounceState{
		WindowStartedTS:               candidate.At,
		HighestPriorityTrigger:        candidate.Reason,
		HighestPriority:               candidate.Priority,
		PendingTrigger:                &candidate,
		PendingSourceEventRange:       []string{first, last},
		PendingSourceStartedAt:        firstEventTime(e.state.LastCheckpointTS, event.Ts),
		PendingSourceEndedAt:          candidate.At,
		PendingTriggerTranscriptID:    event.TranscriptID,
		PendingTriggerHarness:         event.Harness,
		PendingTriggerWorkingDir:      event.WorkingDir,
		PendingTriggerThreadID:        e.state.CurrentThreadID,
		PendingTriggerFilesTouched:    append([]string(nil), e.state.FilesTouched...),
		PendingTriggerCommitRefs:      append([]string(nil), e.state.CommitRefs...),
		PendingTriggerSourceTruncated: false,
	}
}

func firstEventTime(lastCheckpoint time.Time, fallback time.Time) time.Time {
	if !lastCheckpoint.IsZero() {
		return lastCheckpoint
	}
	return fallback
}

func (e *Engine) pendingDue(now time.Time) bool {
	if e.state.Debounce.PendingTrigger == nil {
		return false
	}
	return !now.Before(e.state.Debounce.WindowStartedTS.Add(e.config.DebounceWindow))
}

func (e *Engine) firePending(now time.Time) *CheckpointRequest {
	pending := e.state.Debounce.Normalize()
	if pending.PendingTrigger == nil {
		return nil
	}

	sourceRange := SourceEventRange{}
	if len(pending.PendingSourceEventRange) == 2 {
		sourceRange.First = pending.PendingSourceEventRange[0]
		sourceRange.Last = pending.PendingSourceEventRange[1]
	}

	checkpoint := &CheckpointRequest{
		Trigger:         *pending.PendingTrigger,
		ThreadID:        pending.PendingTriggerThreadID,
		TranscriptID:    pending.PendingTriggerTranscriptID,
		Harness:         pending.PendingTriggerHarness,
		WorkingDir:      pending.PendingTriggerWorkingDir,
		SourceRange:     sourceRange,
		SourceStartedAt: pending.PendingSourceStartedAt,
		SourceEndedAt:   pending.PendingSourceEndedAt,
		FilesTouched:    append([]string(nil), pending.PendingTriggerFilesTouched...),
		CommitRefs:      append([]string(nil), pending.PendingTriggerCommitRefs...),
		SourceTruncated: pending.PendingTriggerSourceTruncated,
	}

	e.state.LastCheckpointTS = normalizeTime(now)
	e.state.TurnsSinceCheckpoint = 0
	e.state.SubstantiveEvents = 0
	e.state.CurrentRangeStartID = ""
	e.state.FilesTouched = nil
	e.state.CommitRefs = nil
	e.state.Debounce = DebounceState{}
	return checkpoint
}

func (e *Engine) recordSourceMetadata(event Event) {
	e.state.FilesTouched = appendUnique(e.state.FilesTouched, filesTouched(event)...)
	e.state.CommitRefs = appendUnique(e.state.CommitRefs, commitRefs(event)...)
}

func (e *Engine) hasProcessedEvent(eventID string) bool {
	if strings.TrimSpace(eventID) == "" {
		return false
	}
	if e.state.LastProcessedEventID == eventID {
		return true
	}
	return containsString(e.state.RecentEventIDs, eventID)
}

func (e *Engine) markProcessedEvent(eventID string) {
	if strings.TrimSpace(eventID) == "" {
		return
	}
	e.state.LastProcessedEventID = eventID
	e.state.RecentEventIDs = appendUnique(e.state.RecentEventIDs, eventID)
	e.state.RecentEventIDs = limitRecentEventIDs(e.state.RecentEventIDs)
}

func NewThreadID(ts time.Time) (string, error) {
	id, err := NewEventID(ts)
	if err != nil {
		return "", err
	}
	return "t_" + strings.TrimPrefix(id, "evt_"), nil
}

func hasNewThreadSignal(event Event) bool {
	text, ok := decodeText(event)
	if !ok || text.Actor != ActorUser {
		return false
	}
	content := strings.ToLower(text.Content)
	return strings.Contains(content, "--new-thread") ||
		strings.Contains(content, "/threadmark:new-thread") ||
		strings.Contains(content, "/threadmark:new_thread")
}

func hasUserCheckpointSignal(event Event) bool {
	text, ok := decodeText(event)
	if !ok || text.Actor != ActorUser {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(text.Content))
	return strings.HasPrefix(content, "/checkpoint") ||
		strings.HasPrefix(content, "/threadmark:checkpoint") ||
		strings.HasPrefix(content, "threadmark checkpoint")
}

func isStopEvent(event Event) bool {
	text, ok := decodeText(event)
	if !ok {
		return false
	}
	if text.Actor != ActorHarness && text.Actor != ActorSystem {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(text.Content))
	return content == "stop" || content == "session_stop" || content == "session-stop"
}

// preCompactCause returns the compaction cause ("manual" or "auto", defaulting
// to "auto") and true when the event is a harness-emitted pre-compaction
// signal. Content shape: "pre_compact" optionally followed by "cause=<value>"
// and other key=value pairs.
func preCompactCause(event Event) (string, bool) {
	text, ok := decodeText(event)
	if !ok {
		return "", false
	}
	if text.Actor != ActorHarness && text.Actor != ActorSystem {
		return "", false
	}
	content := strings.TrimSpace(strings.ToLower(text.Content))
	if !strings.HasPrefix(content, "pre_compact") {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(content, "pre_compact"))
	cause := "auto"
	for _, field := range strings.Fields(rest) {
		if value, ok := strings.CutPrefix(field, "cause="); ok && value != "" {
			cause = value
		}
	}
	return cause, true
}

func isTurnEvent(event Event) bool {
	text, ok := decodeText(event)
	return ok && text.Actor == ActorUser
}

func isSubstantiveEvent(event Event) bool {
	switch event.Kind {
	case KindToolCall, KindToolResult, KindFileChange, KindWorkingDirChange, KindToolDef:
		return true
	case KindText:
		text, ok := decodeText(event)
		return ok && text.Actor == ActorUser
	default:
		return false
	}
}

func (e *Engine) hasSubstantiveRange() bool {
	return e.state.SubstantiveEvents > 0 || e.state.Debounce.PendingTrigger != nil
}

func decodeText(event Event) (TextPayload, bool) {
	if event.Kind != KindText {
		return TextPayload{}, false
	}
	payload, err := DecodePayload[TextPayload](event.Payload)
	if err != nil {
		return TextPayload{}, false
	}
	return payload, true
}

func filesTouched(event Event) []string {
	if event.Kind != KindFileChange {
		return nil
	}
	payload, err := DecodePayload[FileChangePayload](event.Payload)
	if err != nil || payload.Path == "" {
		return nil
	}
	return []string{payload.Path}
}

func commitRefs(event Event) []string {
	if event.Kind != KindFileChange {
		return nil
	}
	payload, err := DecodePayload[FileChangePayload](event.Payload)
	if err != nil || payload.GitCommit == "" {
		return nil
	}
	return []string{payload.GitCommit}
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		if strings.TrimSpace(addition) == "" || containsString(values, addition) {
			continue
		}
		values = append(values, addition)
	}
	return values
}

func uniqueStrings(values []string) []string {
	return appendUnique(nil, values...)
}

func limitRecentEventIDs(values []string) []string {
	if len(values) <= recentEventIDLimit {
		return values
	}
	return append([]string(nil), values[len(values)-recentEventIDLimit:]...)
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func normalizeTime(ts time.Time) time.Time {
	if ts.IsZero() {
		return ts
	}
	return ts.UTC()
}

func (d DebounceState) MarshalJSON() ([]byte, error) {
	type debounceState DebounceState
	if d.PendingTrigger == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(debounceState(d.Normalize()))
}
