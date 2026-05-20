package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
	"github.com/thinkwright/threadmark/internal/journal"
	"github.com/thinkwright/threadmark/internal/reflector"
)

type Ingestor struct {
	store   journal.Store
	config  core.Config
	engines map[string]*core.Engine
	buffers map[string][]eventExcerpt
	known   map[string]string
	mu      sync.Mutex
}

type IngestResult struct {
	ProjectHash string            `json:"project_hash"`
	WorkingDir  string            `json:"working_dir"`
	Excerpt     string            `json:"excerpt,omitempty"`
	Result      core.IngestResult `json:"result"`
}

func NewIngestor(store journal.Store, config core.Config) *Ingestor {
	return &Ingestor{
		store:   store,
		config:  config,
		engines: make(map[string]*core.Engine),
		buffers: make(map[string][]eventExcerpt),
		known:   make(map[string]string),
	}
}

func (i *Ingestor) Ingest(_ context.Context, event core.Event) (IngestResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	hash, resolvedWorkingDir, err := i.store.ProjectID(event.WorkingDir)
	if err != nil {
		return IngestResult{}, err
	}
	event.WorkingDir = resolvedWorkingDir
	event = event.WithoutRaw()

	engine, err := i.engine(hash, resolvedWorkingDir)
	if err != nil {
		return IngestResult{}, err
	}

	result, err := engine.Ingest(event)
	if err != nil {
		return IngestResult{}, err
	}
	i.known[hash] = resolvedWorkingDir
	if !result.Duplicate {
		i.appendExcerpt(hash, event)
	}

	state := result.State
	state.ProjectHash = hash
	state.WorkingDir = resolvedWorkingDir
	if err := i.saveState(state); err != nil {
		return IngestResult{}, err
	}
	result.State = state

	excerpt := ""
	if result.Checkpoint != nil {
		excerpt = i.excerptForCheckpoint(hash, *result.Checkpoint)
	}

	return IngestResult{
		ProjectHash: hash,
		WorkingDir:  resolvedWorkingDir,
		Excerpt:     excerpt,
		Result:      result,
	}, nil
}

func (i *Ingestor) FlushDue(_ context.Context, workingDir string, now time.Time) (*IngestResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	hash, resolvedWorkingDir, err := i.store.ProjectID(workingDir)
	if err != nil {
		return nil, err
	}

	engine, err := i.engine(hash, resolvedWorkingDir)
	if err != nil {
		return nil, err
	}

	checkpoint := engine.FlushDue(now)
	if checkpoint == nil {
		return nil, nil
	}

	state := engine.State()
	state.ProjectHash = hash
	state.WorkingDir = resolvedWorkingDir
	if err := i.saveState(state); err != nil {
		return nil, err
	}
	excerpt := i.excerptForCheckpoint(hash, *checkpoint)
	return &IngestResult{
		ProjectHash: hash,
		WorkingDir:  resolvedWorkingDir,
		Excerpt:     excerpt,
		Result: core.IngestResult{
			ThreadID:   checkpoint.ThreadID,
			Checkpoint: checkpoint,
			State:      state,
		},
	}, nil
}

func (i *Ingestor) CheckIdleAll(_ context.Context, now time.Time) ([]IngestResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	results := make([]IngestResult, 0)
	for hash, workingDir := range i.known {
		engine, err := i.engine(hash, workingDir)
		if err != nil {
			return nil, err
		}
		decision, checkpoint := engine.CheckIdle(now)
		if decision == nil && checkpoint == nil {
			continue
		}

		state := engine.State()
		state.ProjectHash = hash
		state.WorkingDir = workingDir
		if err := i.saveState(state); err != nil {
			return nil, err
		}

		result := core.IngestResult{
			ThreadID:        state.CurrentThreadID,
			TriggerDecision: core.TriggerDecision{},
			Checkpoint:      checkpoint,
			State:           state,
		}
		if decision != nil {
			result.TriggerDecision = *decision
			result.TriggerCandidate = decision.Candidate
		}

		excerpt := ""
		if checkpoint != nil {
			excerpt = i.excerptForCheckpoint(hash, *checkpoint)
		}

		results = append(results, IngestResult{
			ProjectHash: hash,
			WorkingDir:  workingDir,
			Excerpt:     excerpt,
			Result:      result,
		})
	}
	return results, nil
}

func (i *Ingestor) CompleteCheckpoint(result IngestResult) {
	checkpoint := result.Result.Checkpoint
	if checkpoint == nil {
		return
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	i.discardThrough(result.ProjectHash, checkpoint.SourceRange.Last)
}

func (i *Ingestor) engine(projectHash, workingDir string) (*core.Engine, error) {
	if engine := i.engines[projectHash]; engine != nil {
		return engine, nil
	}

	state, err := i.loadState(workingDir)
	if err != nil {
		return nil, err
	}
	state.ProjectHash = projectHash
	state.WorkingDir = workingDir

	engine := core.NewEngine(state, i.config)
	i.engines[projectHash] = engine
	i.known[projectHash] = workingDir
	return engine, nil
}

func (i *Ingestor) loadState(workingDir string) (core.ProjectState, error) {
	path, resolvedWorkingDir, err := i.store.StatePath(workingDir)
	if err != nil {
		return core.ProjectState{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return core.ProjectState{WorkingDir: resolvedWorkingDir}, nil
	}
	if err != nil {
		return core.ProjectState{}, fmt.Errorf("read state: %w", err)
	}

	var state core.ProjectState
	if err := json.Unmarshal(data, &state); err != nil {
		return core.ProjectState{}, fmt.Errorf("decode state: %w", err)
	}
	state.WorkingDir = resolvedWorkingDir
	return dropTransientCheckpointState(state.Normalize()), nil
}

func dropTransientCheckpointState(state core.ProjectState) core.ProjectState {
	state.CurrentRangeStartID = ""
	state.TurnsSinceCheckpoint = 0
	state.SubstantiveEvents = 0
	state.FilesTouched = nil
	state.CommitRefs = nil
	state.Debounce = core.DebounceState{}
	return state
}

func (i *Ingestor) saveState(state core.ProjectState) error {
	path, _, err := i.store.StatePath(state.WorkingDir)
	if err != nil {
		return err
	}
	if _, _, err := i.store.EnsureProject(state.WorkingDir); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state.Normalize(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create state tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write state tempfile: %w", err)
	}
	if err := tmp.Chmod(journal.FileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set state tempfile permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close state tempfile: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	if err := os.Chmod(path, journal.FileMode); err != nil {
		return fmt.Errorf("set state permissions: %w", err)
	}
	return nil
}

type eventExcerpt struct {
	EventID string
	Text    string
}

func (i *Ingestor) appendExcerpt(projectHash string, event core.Event) {
	excerpt := summarizeEvent(event)
	if excerpt == "" {
		return
	}
	i.buffers[projectHash] = append(i.buffers[projectHash], eventExcerpt{
		EventID: event.EventID,
		Text:    excerpt,
	})
	if len(i.buffers[projectHash]) > 500 {
		i.buffers[projectHash] = append([]eventExcerpt(nil), i.buffers[projectHash][len(i.buffers[projectHash])-500:]...)
	}
}

func (i *Ingestor) excerptForCheckpoint(projectHash string, checkpoint core.CheckpointRequest) string {
	buffer := i.buffers[projectHash]
	if len(buffer) == 0 {
		return ""
	}

	var b strings.Builder
	inRange := checkpoint.SourceRange.First == ""
	for _, record := range buffer {
		if !inRange && record.EventID == checkpoint.SourceRange.First {
			inRange = true
		}
		if !inRange {
			continue
		}
		b.WriteString(record.Text)
		if !strings.HasSuffix(record.Text, "\n") {
			b.WriteByte('\n')
		}
		if checkpoint.SourceRange.Last != "" && record.EventID == checkpoint.SourceRange.Last {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func (i *Ingestor) discardThrough(projectHash, eventID string) {
	if eventID == "" {
		return
	}
	buffer := i.buffers[projectHash]
	for idx, record := range buffer {
		if record.EventID == eventID {
			i.buffers[projectHash] = append([]eventExcerpt(nil), buffer[idx+1:]...)
			return
		}
	}
}

func summarizeEvent(event core.Event) string {
	ts := event.Ts.Format(time.RFC3339Nano)
	prefix := fmt.Sprintf("- [%s] %s %s %s: ", ts, event.EventID, event.Harness, event.Kind)
	switch event.Kind {
	case core.KindText:
		payload, err := core.DecodePayload[core.TextPayload](event.Payload)
		if err != nil {
			return prefix + "text payload decode failed"
		}
		return prefix + fmt.Sprintf("%s said: %s", payload.Actor, reflector.Redact(payload.Content))
	case core.KindToolCall:
		payload, err := core.DecodePayload[core.ToolCallPayload](event.Payload)
		if err != nil {
			return prefix + "tool_call payload decode failed"
		}
		return prefix + fmt.Sprintf("tool call %q correlation_id=%s args=<omitted>", payload.Tool, payload.CorrelationID)
	case core.KindToolResult:
		payload, err := core.DecodePayload[core.ToolResultPayload](event.Payload)
		if err != nil {
			return prefix + "tool_result payload decode failed"
		}
		summary := reflector.Redact(payload.OutputSummary)
		if summary == "" && payload.Error != "" {
			summary = "error: " + reflector.Redact(payload.Error)
		}
		return prefix + fmt.Sprintf("tool result correlation_id=%s status=%s summary=%s truncated=%t", payload.CorrelationID, payload.Status, summary, payload.Truncated)
	case core.KindFileChange:
		payload, err := core.DecodePayload[core.FileChangePayload](event.Payload)
		if err != nil {
			return prefix + "file_change payload decode failed"
		}
		return prefix + fmt.Sprintf("file %s %s source=%s commit=%s", payload.Op, payload.Path, payload.Source, payload.GitCommit)
	case core.KindWorkingDirChange:
		payload, err := core.DecodePayload[core.WorkingDirChangePayload](event.Payload)
		if err != nil {
			return prefix + "working_dir_change payload decode failed"
		}
		return prefix + fmt.Sprintf("working dir changed from %s to %s", payload.From, payload.To)
	case core.KindToolDef:
		payload, err := core.DecodePayload[core.ToolDefPayload](event.Payload)
		if err != nil {
			return prefix + "tool_def payload decode failed"
		}
		return prefix + fmt.Sprintf("tool definition %q from %s schema=<omitted>", payload.Name, payload.SourceHarness)
	default:
		return prefix + "unknown event"
	}
}
