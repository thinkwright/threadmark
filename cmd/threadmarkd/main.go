// Command threadmarkd is the Threadmark daemon: a long-running per-user process
// that receives events from hook shims over a unix domain socket, classifies
// salient triggers, debounces, and invokes the reflector to write journal
// entries. See HOW_IT_WORKS.md for the shim+daemon architecture.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/thinkwright/threadmark/internal/buildinfo"
	"github.com/thinkwright/threadmark/internal/core"
	"github.com/thinkwright/threadmark/internal/daemon"
	"github.com/thinkwright/threadmark/internal/ipc"
	"github.com/thinkwright/threadmark/internal/journal"
	"github.com/thinkwright/threadmark/internal/reflector"
)

func main() {
	root := flag.String("root", "", "threadmark root directory (default ~/.threadmark)")
	socketPath := flag.String("socket", "", "unix socket path (default ~/.threadmark/daemon.sock)")
	promptPath := flag.String("reflector-prompt", reflector.DefaultPromptPath(), "reflector prompt path")
	reflectorCommand := flag.String("reflector-command", reflector.DefaultCommand, "reflector CLI command")
	reflectorMode := flag.String("reflector-mode", envDefault("THREADMARK_REFLECTOR_MODE", string(reflector.ModeConvenience)), "reflector mode: convenience or bare")
	reflectorModel := flag.String("reflector-model", reflector.DefaultModel, "reflector model metadata")
	reflectorTimeout := flag.Duration("reflector-timeout", envDuration("THREADMARK_REFLECTOR_TIMEOUT", 2*time.Minute), "maximum time for a reflector call or queue wait; 0 disables timeout")
	reflectorConcurrency := flag.Int("reflector-concurrency", envInt("THREADMARK_REFLECTOR_CONCURRENCY", 1), "maximum concurrent reflector calls")
	checkpointRetryInitialBackoff := flag.Duration("checkpoint-retry-initial-backoff", envDuration("THREADMARK_CHECKPOINT_RETRY_INITIAL_BACKOFF", 30*time.Second), "initial backoff before retrying a failed checkpoint")
	checkpointRetryMaxBackoff := flag.Duration("checkpoint-retry-max-backoff", envDuration("THREADMARK_CHECKPOINT_RETRY_MAX_BACKOFF", 5*time.Minute), "maximum backoff between failed checkpoint retries")
	noJournal := flag.Bool("no-journal", envBool("THREADMARK_NO_JOURNAL"), "disable reflector and journal writes")
	showVersion := flag.Bool("version", false, "print Threadmark daemon version")
	tickInterval := flag.Duration("tick-interval", 30*time.Second, "idle/debounce check interval; 0 disables timer")
	threadBoundaryAfter := flag.Duration("thread-boundary-after", core.DefaultThreadBoundaryAfter, "new thread after this inactivity gap")
	idleTriggerAfter := flag.Duration("idle-trigger-after", core.DefaultIdleTriggerAfter, "idle trigger after this inactivity gap")
	debounceWindow := flag.Duration("debounce-window", core.DefaultDebounceWindow, "trigger debounce window")
	safetyNetTurns := flag.Int("safety-net-turns", core.DefaultSafetyNetTurns, "user turns before safety-net trigger")
	flag.Parse()
	if *showVersion {
		fmt.Fprintln(os.Stdout, buildinfo.Version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resolvedRoot := *root
	if resolvedRoot == "" {
		var err error
		resolvedRoot, err = journal.DefaultRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "threadmarkd: %v\n", err)
			os.Exit(1)
		}
	}

	resolvedSocketPath := *socketPath
	if resolvedSocketPath == "" {
		resolvedSocketPath = filepath.Join(resolvedRoot, ipc.DefaultSocket)
	}

	logger := newEventLogger(os.Stdout)
	store := journal.NewStore(resolvedRoot)
	ingestor := daemon.NewIngestor(store, core.Config{
		ThreadBoundaryAfter: *threadBoundaryAfter,
		IdleTriggerAfter:    *idleTriggerAfter,
		DebounceWindow:      *debounceWindow,
		SafetyNetTurns:      *safetyNetTurns,
	})
	mode, err := reflector.ParseMode(*reflectorMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "threadmarkd: %v\n", err)
		os.Exit(1)
	}
	reflectorClient, err := reflector.NewClient(reflector.Config{
		Command:    *reflectorCommand,
		Mode:       mode,
		Model:      *reflectorModel,
		PromptPath: *promptPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "threadmarkd: %v\n", err)
		os.Exit(1)
	}
	checkpoints := checkpointHandler{
		store:            store,
		reflector:        reflectorClient,
		logger:           logger,
		noJournal:        *noJournal,
		reflectorTimeout: *reflectorTimeout,
		reflectorSlots:   make(chan struct{}, max(1, *reflectorConcurrency)),
	}
	checkpointRetries := newCheckpointRetrier(ctx, ingestor, checkpoints, logger, *checkpointRetryInitialBackoff, *checkpointRetryMaxBackoff)

	if *tickInterval > 0 {
		go runIdleTicker(ctx, *tickInterval, ingestor, checkpoints, checkpointRetries, logger)
	}

	server := ipc.Server{
		SocketPath: resolvedSocketPath,
		Handler: ipc.HandlerFunc(func(ctx context.Context, event core.Event) error {
			result, err := ingestor.Ingest(ctx, event)
			if err != nil {
				return err
			}
			if err := logger.LogIngest(result); err != nil {
				return err
			}
			return handleCheckpoint(ctx, ingestor, checkpoints, checkpointRetries, result)
		}),
		ErrorHandler: func(err error) {
			_ = logger.LogError(err)
		},
	}

	fmt.Fprintf(os.Stderr, "threadmarkd: listening on %s\n", resolvedSocketPath)
	if err := server.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "threadmarkd: %v\n", err)
		os.Exit(1)
	}
}

func runIdleTicker(ctx context.Context, interval time.Duration, ingestor *daemon.Ingestor, checkpoints checkpointHandler, checkpointRetries *checkpointRetrier, logger *eventLogger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			results, err := ingestor.CheckIdleAll(ctx, now.UTC())
			if err != nil {
				_ = logger.LogError(err)
				continue
			}
			for _, result := range results {
				if err := logger.LogIngest(result); err != nil {
					_ = logger.LogError(err)
					continue
				}
				if err := handleCheckpoint(ctx, ingestor, checkpoints, checkpointRetries, result); err != nil {
					_ = logger.LogError(err)
				}
			}
		}
	}
}

func handleCheckpoint(ctx context.Context, ingestor *daemon.Ingestor, checkpoints checkpointHandler, checkpointRetries *checkpointRetrier, result daemon.IngestResult) error {
	if err := checkpoints.Handle(ctx, result); err != nil {
		if checkpointRetries != nil && ctx.Err() == nil {
			checkpointRetries.Schedule(result, err)
			return nil
		}
		return err
	}
	ingestor.CompleteCheckpoint(result)
	return nil
}

type checkpointRetrier struct {
	ctx            context.Context
	ingestor       *daemon.Ingestor
	checkpoints    checkpointHandler
	logger         *eventLogger
	initialBackoff time.Duration
	maxBackoff     time.Duration
	mu             sync.Mutex
	pending        map[string]*checkpointRetry
}

type checkpointRetry struct {
	result   daemon.IngestResult
	attempts int
	backoff  time.Duration
}

func newCheckpointRetrier(ctx context.Context, ingestor *daemon.Ingestor, checkpoints checkpointHandler, logger *eventLogger, initialBackoff, maxBackoff time.Duration) *checkpointRetrier {
	if initialBackoff <= 0 {
		initialBackoff = 30 * time.Second
	}
	if maxBackoff <= 0 {
		maxBackoff = initialBackoff
	}
	if maxBackoff < initialBackoff {
		maxBackoff = initialBackoff
	}
	return &checkpointRetrier{
		ctx:            ctx,
		ingestor:       ingestor,
		checkpoints:    checkpoints,
		logger:         logger,
		initialBackoff: initialBackoff,
		maxBackoff:     maxBackoff,
		pending:        make(map[string]*checkpointRetry),
	}
}

func (r *checkpointRetrier) Schedule(result daemon.IngestResult, cause error) {
	if r == nil || result.Result.Checkpoint == nil || r.ctx.Err() != nil {
		return
	}

	key := checkpointRetryKey(result)
	r.mu.Lock()
	if _, exists := r.pending[key]; exists {
		r.mu.Unlock()
		return
	}
	retry := &checkpointRetry{
		result:  result,
		backoff: r.initialBackoff,
	}
	r.pending[key] = retry
	r.mu.Unlock()

	checkpoint := result.Result.Checkpoint
	_ = r.logger.LogCheckpointRetryScheduled(result.ProjectHash, checkpoint.ThreadID, checkpoint.Trigger.Reason, retry.backoff, cause)
	go r.run(key, retry)
}

func (r *checkpointRetrier) run(key string, retry *checkpointRetry) {
	delay := retry.backoff
	for {
		timer := time.NewTimer(delay)
		select {
		case <-r.ctx.Done():
			timer.Stop()
			r.remove(key)
			return
		case <-timer.C:
		}

		retry.attempts++
		checkpoint := retry.result.Result.Checkpoint
		if checkpoint == nil {
			r.remove(key)
			return
		}
		if err := r.checkpoints.Handle(r.ctx, retry.result); err != nil {
			nextBackoff := minDuration(delay*2, r.maxBackoff)
			_ = r.logger.LogCheckpointRetryFailed(retry.result.ProjectHash, checkpoint.ThreadID, checkpoint.Trigger.Reason, retry.attempts, nextBackoff, err)
			delay = nextBackoff
			continue
		}

		r.ingestor.CompleteCheckpoint(retry.result)
		r.remove(key)
		_ = r.logger.LogCheckpointRetryCompleted(retry.result.ProjectHash, checkpoint.ThreadID, checkpoint.Trigger.Reason, retry.attempts)
		return
	}
}

func (r *checkpointRetrier) remove(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, key)
}

func checkpointRetryKey(result daemon.IngestResult) string {
	checkpoint := result.Result.Checkpoint
	if checkpoint == nil {
		return ""
	}
	return strings.Join([]string{
		result.ProjectHash,
		checkpoint.ThreadID,
		checkpoint.Trigger.Reason,
		checkpoint.SourceRange.First,
		checkpoint.SourceRange.Last,
	}, "\x00")
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

type checkpointHandler struct {
	store            journal.Store
	reflector        *reflector.Client
	logger           *eventLogger
	noJournal        bool
	reflectorTimeout time.Duration
	reflectorSlots   chan struct{}
}

func (h checkpointHandler) Handle(ctx context.Context, result daemon.IngestResult) error {
	checkpoint := result.Result.Checkpoint
	if checkpoint == nil {
		return nil
	}
	if h.noJournal {
		return h.logger.LogJournalSkipped(result.ProjectHash, checkpoint.ThreadID, checkpoint.Trigger.Reason)
	}
	if strings.TrimSpace(result.Excerpt) == "" {
		return h.logger.LogJournalSkippedReason(result.ProjectHash, checkpoint.ThreadID, checkpoint.Trigger.Reason, "empty_excerpt")
	}

	release, err := h.acquireReflectorSlot(ctx)
	if err != nil {
		_ = h.logger.LogReflectorBackoff(result.ProjectHash, checkpoint.ThreadID, checkpoint.Trigger.Reason, err)
		return err
	}
	defer release()

	if err := h.logger.LogReflectorStarted(result.ProjectHash, checkpoint.ThreadID, checkpoint.Trigger.Reason); err != nil {
		return err
	}

	reflectCtx := ctx
	var cancel context.CancelFunc
	if h.reflectorTimeout > 0 {
		reflectCtx, cancel = context.WithTimeout(ctx, h.reflectorTimeout)
		defer cancel()
	}

	response, err := h.reflector.Reflect(reflectCtx, reflector.Request{
		Checkpoint: *checkpoint,
		Transcript: result.Excerpt,
	})
	if err != nil {
		if reflectCtx.Err() == context.DeadlineExceeded {
			_ = h.logger.LogReflectorTimedOut(result.ProjectHash, checkpoint.ThreadID, checkpoint.Trigger.Reason, h.reflectorTimeout)
		}
		_ = h.logger.LogReflectorFailed(result.ProjectHash, checkpoint.ThreadID, err)
		return err
	}
	if err := h.logger.LogReflectorCompleted(result.ProjectHash, checkpoint.ThreadID, response); err != nil {
		return err
	}

	entryID, err := journal.NewEntryID(response.CompletedAt)
	if err != nil {
		_ = h.logger.LogJournalFailed(result.ProjectHash, checkpoint.ThreadID, err)
		return err
	}
	entry := journal.Entry{
		Frontmatter: journal.Frontmatter{
			EntryID:              entryID,
			ThreadID:             checkpoint.ThreadID,
			TranscriptID:         checkpoint.TranscriptID,
			Trigger:              checkpoint.Trigger.Reason,
			Timestamp:            checkpoint.Trigger.At,
			SourceEventRange:     []string{checkpoint.SourceRange.First, checkpoint.SourceRange.Last},
			SourceStartedAt:      checkpoint.SourceStartedAt,
			SourceEndedAt:        checkpoint.SourceEndedAt,
			SourceTruncated:      checkpoint.SourceTruncated,
			Harness:              checkpoint.Harness,
			WorkingDir:           checkpoint.WorkingDir,
			FilesTouched:         checkpoint.FilesTouched,
			CommitRefs:           checkpoint.CommitRefs,
			ReflectorBackend:     response.Backend,
			ReflectorCommandMode: string(response.CommandMode),
			ReflectorModel:       response.Model,
			ReflectorPromptHash:  response.PromptHash,
			ThreadmarkVersion:    buildinfo.Version,
		},
		Body: response.Body,
	}
	markdown, err := entry.Markdown()
	if err != nil {
		_ = h.logger.LogJournalFailed(result.ProjectHash, checkpoint.ThreadID, err)
		return err
	}
	if _, err := h.store.Append(checkpoint.WorkingDir, entry); err != nil {
		_ = h.logger.LogJournalFailed(result.ProjectHash, checkpoint.ThreadID, err)
		return err
	}
	return h.logger.LogJournalCompleted(result.ProjectHash, checkpoint.ThreadID, entryID, len(markdown))
}

func (h checkpointHandler) acquireReflectorSlot(ctx context.Context) (func(), error) {
	if h.reflectorSlots == nil {
		return func() {}, nil
	}

	waitCtx := ctx
	var cancel context.CancelFunc
	if h.reflectorTimeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, h.reflectorTimeout)
		defer cancel()
	}

	select {
	case h.reflectorSlots <- struct{}{}:
		return func() { <-h.reflectorSlots }, nil
	case <-waitCtx.Done():
		return nil, fmt.Errorf("wait for reflector slot: %w", waitCtx.Err())
	}
}

func (l *eventLogger) LogIngest(result daemon.IngestResult) error {
	if result.Result.EventID != "" {
		if err := l.LogEventReceived(result); err != nil {
			return err
		}
	}
	if boundary := result.Result.ThreadBoundary; boundary != nil {
		if err := l.log(struct {
			Ts        time.Time                 `json:"ts"`
			Kind      string                    `json:"kind"`
			ProjectID string                    `json:"project_id"`
			ThreadID  string                    `json:"thread_id"`
			Reason    core.ThreadBoundaryReason `json:"reason"`
			EventID   string                    `json:"event_id"`
		}{
			Ts:        boundary.At,
			Kind:      "thread.boundary",
			ProjectID: result.ProjectHash,
			ThreadID:  boundary.ThreadID,
			Reason:    boundary.Reason,
			EventID:   result.Result.EventID,
		}); err != nil {
			return err
		}
	}
	if candidate := result.Result.TriggerCandidate; candidate != nil {
		if err := l.log(struct {
			Ts        time.Time        `json:"ts"`
			Kind      string           `json:"kind"`
			ProjectID string           `json:"project_id"`
			ThreadID  string           `json:"thread_id"`
			Trigger   core.TriggerType `json:"trigger"`
			Reason    string           `json:"reason"`
			Priority  int              `json:"priority"`
			EventID   string           `json:"event_id"`
		}{
			Ts:        candidate.At,
			Kind:      "trigger.candidate",
			ProjectID: result.ProjectHash,
			ThreadID:  result.Result.ThreadID,
			Trigger:   candidate.Type,
			Reason:    candidate.Reason,
			Priority:  candidate.Priority,
			EventID:   candidate.EventID,
		}); err != nil {
			return err
		}
	}
	if decision := result.Result.TriggerDecision; decision.Kind == core.TriggerDecisionSkipped || decision.Kind == core.TriggerDecisionReplaced {
		if err := l.log(struct {
			Ts          time.Time                `json:"ts"`
			Kind        string                   `json:"kind"`
			ProjectID   string                   `json:"project_id"`
			ThreadID    string                   `json:"thread_id"`
			Decision    core.TriggerDecisionKind `json:"decision"`
			Description string                   `json:"description,omitempty"`
			EventID     string                   `json:"event_id"`
		}{
			Ts:          time.Now().UTC(),
			Kind:        "trigger.skipped",
			ProjectID:   result.ProjectHash,
			ThreadID:    result.Result.ThreadID,
			Decision:    decision.Kind,
			Description: decision.Description,
			EventID:     result.Result.EventID,
		}); err != nil {
			return err
		}
	}
	if checkpoint := result.Result.Checkpoint; checkpoint != nil {
		if err := l.log(struct {
			Ts        time.Time             `json:"ts"`
			Kind      string                `json:"kind"`
			ProjectID string                `json:"project_id"`
			ThreadID  string                `json:"thread_id"`
			Trigger   core.TriggerType      `json:"trigger"`
			Reason    string                `json:"reason"`
			Range     core.SourceEventRange `json:"source_event_range"`
		}{
			Ts:        time.Now().UTC(),
			Kind:      "trigger.fired",
			ProjectID: result.ProjectHash,
			ThreadID:  checkpoint.ThreadID,
			Trigger:   checkpoint.Trigger.Type,
			Reason:    checkpoint.Trigger.Reason,
			Range:     checkpoint.SourceRange,
		}); err != nil {
			return err
		}
	}
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		EventID   string    `json:"event_id"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "state.write_completed",
		ProjectID: result.ProjectHash,
		ThreadID:  result.Result.ThreadID,
		EventID:   result.Result.EventID,
	})
}

func (l *eventLogger) LogError(err error) error {
	return l.log(struct {
		Ts    time.Time `json:"ts"`
		Kind  string    `json:"kind"`
		Error string    `json:"error"`
	}{
		Ts:    time.Now().UTC(),
		Kind:  "event.rejected",
		Error: err.Error(),
	})
}

func (l *eventLogger) LogReflectorStarted(projectID, threadID, trigger string) error {
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		Trigger   string    `json:"trigger"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "reflector.started",
		ProjectID: projectID,
		ThreadID:  threadID,
		Trigger:   trigger,
	})
}

func (l *eventLogger) LogReflectorCompleted(projectID, threadID string, response reflector.Response) error {
	return l.log(struct {
		Ts          time.Time      `json:"ts"`
		Kind        string         `json:"kind"`
		ProjectID   string         `json:"project_id"`
		ThreadID    string         `json:"thread_id"`
		Mode        reflector.Mode `json:"mode"`
		Model       string         `json:"model"`
		PromptHash  string         `json:"prompt_hash"`
		OutputBytes int            `json:"output_bytes"`
	}{
		Ts:          response.CompletedAt,
		Kind:        "reflector.completed",
		ProjectID:   projectID,
		ThreadID:    threadID,
		Mode:        response.CommandMode,
		Model:       response.Model,
		PromptHash:  response.PromptHash,
		OutputBytes: len(response.Body),
	})
}

func (l *eventLogger) LogReflectorFailed(projectID, threadID string, err error) error {
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		Error     string    `json:"error"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "reflector.failed",
		ProjectID: projectID,
		ThreadID:  threadID,
		Error:     err.Error(),
	})
}

func (l *eventLogger) LogReflectorTimedOut(projectID, threadID, trigger string, timeout time.Duration) error {
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		Trigger   string    `json:"trigger"`
		TimeoutMS int64     `json:"timeout_ms"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "reflector.timed_out",
		ProjectID: projectID,
		ThreadID:  threadID,
		Trigger:   trigger,
		TimeoutMS: timeout.Milliseconds(),
	})
}

func (l *eventLogger) LogReflectorBackoff(projectID, threadID, trigger string, err error) error {
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		Trigger   string    `json:"trigger"`
		Error     string    `json:"error"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "reflector.backoff",
		ProjectID: projectID,
		ThreadID:  threadID,
		Trigger:   trigger,
		Error:     err.Error(),
	})
}

func (l *eventLogger) LogJournalCompleted(projectID, threadID, entryID string, bytes int) error {
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		EntryID   string    `json:"entry_id"`
		Bytes     int       `json:"bytes"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "journal.write_completed",
		ProjectID: projectID,
		ThreadID:  threadID,
		EntryID:   entryID,
		Bytes:     bytes,
	})
}

func (l *eventLogger) LogJournalFailed(projectID, threadID string, err error) error {
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		Error     string    `json:"error"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "journal.write_failed",
		ProjectID: projectID,
		ThreadID:  threadID,
		Error:     err.Error(),
	})
}

func (l *eventLogger) LogJournalSkipped(projectID, threadID, trigger string) error {
	return l.LogJournalSkippedReason(projectID, threadID, trigger, "no_journal")
}

func (l *eventLogger) LogJournalSkippedReason(projectID, threadID, trigger, reason string) error {
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		Trigger   string    `json:"trigger"`
		Reason    string    `json:"reason"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "journal.skipped",
		ProjectID: projectID,
		ThreadID:  threadID,
		Trigger:   trigger,
		Reason:    reason,
	})
}

func (l *eventLogger) LogCheckpointRetryScheduled(projectID, threadID, trigger string, backoff time.Duration, err error) error {
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		Trigger   string    `json:"trigger"`
		BackoffMS int64     `json:"backoff_ms"`
		Error     string    `json:"error"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "checkpoint.retry_scheduled",
		ProjectID: projectID,
		ThreadID:  threadID,
		Trigger:   trigger,
		BackoffMS: backoff.Milliseconds(),
		Error:     errorMessage,
	})
}

func (l *eventLogger) LogCheckpointRetryFailed(projectID, threadID, trigger string, attempt int, nextBackoff time.Duration, err error) error {
	return l.log(struct {
		Ts            time.Time `json:"ts"`
		Kind          string    `json:"kind"`
		ProjectID     string    `json:"project_id"`
		ThreadID      string    `json:"thread_id"`
		Trigger       string    `json:"trigger"`
		Attempt       int       `json:"attempt"`
		NextBackoffMS int64     `json:"next_backoff_ms"`
		Error         string    `json:"error"`
	}{
		Ts:            time.Now().UTC(),
		Kind:          "checkpoint.retry_failed",
		ProjectID:     projectID,
		ThreadID:      threadID,
		Trigger:       trigger,
		Attempt:       attempt,
		NextBackoffMS: nextBackoff.Milliseconds(),
		Error:         err.Error(),
	})
}

func (l *eventLogger) LogCheckpointRetryCompleted(projectID, threadID, trigger string, attempt int) error {
	return l.log(struct {
		Ts        time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		ProjectID string    `json:"project_id"`
		ThreadID  string    `json:"thread_id"`
		Trigger   string    `json:"trigger"`
		Attempt   int       `json:"attempt"`
	}{
		Ts:        time.Now().UTC(),
		Kind:      "checkpoint.retry_completed",
		ProjectID: projectID,
		ThreadID:  threadID,
		Trigger:   trigger,
		Attempt:   attempt,
	})
}

type eventLogger struct {
	encoder *json.Encoder
	mu      sync.Mutex
}

func newEventLogger(output *os.File) *eventLogger {
	return &eventLogger{encoder: json.NewEncoder(output)}
}

func (l *eventLogger) LogEventReceived(result daemon.IngestResult) error {
	return l.log(struct {
		Ts           time.Time      `json:"ts"`
		Kind         string         `json:"kind"`
		ProjectID    string         `json:"project_id"`
		ThreadID     string         `json:"thread_id"`
		EventID      string         `json:"event_id"`
		EventKind    core.EventKind `json:"event_kind"`
		Harness      string         `json:"harness"`
		TranscriptID string         `json:"transcript_id"`
		WorkingDir   string         `json:"working_dir"`
		SourceSeq    int64          `json:"source_seq,omitempty"`
	}{
		Ts:           time.Now().UTC(),
		Kind:         "event.received",
		ProjectID:    result.ProjectHash,
		ThreadID:     result.Result.ThreadID,
		EventID:      result.Result.EventID,
		EventKind:    result.Result.EventKind,
		Harness:      result.Result.Harness,
		TranscriptID: result.Result.TranscriptID,
		WorkingDir:   result.WorkingDir,
		SourceSeq:    result.Result.SourceSeq,
	})
}

func (l *eventLogger) log(value any) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.encoder.Encode(value)
}

func envDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
