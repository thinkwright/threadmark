package core

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const CurrentSchemaVersion = 1

type EventKind string

const (
	KindText             EventKind = "text"
	KindToolCall         EventKind = "tool_call"
	KindToolResult       EventKind = "tool_result"
	KindFileChange       EventKind = "file_change"
	KindWorkingDirChange EventKind = "working_dir_change"
	KindToolDef          EventKind = "tool_def"
)

func (k EventKind) Valid() bool {
	switch k {
	case KindText, KindToolCall, KindToolResult, KindFileChange, KindWorkingDirChange, KindToolDef:
		return true
	default:
		return false
	}
}

type Event struct {
	SchemaVersion int             `json:"schema_version"`
	EventID       string          `json:"event_id"`
	Ts            time.Time       `json:"ts"`
	Harness       string          `json:"harness"`
	TranscriptID  string          `json:"transcript_id"`
	SourceSeq     int64           `json:"source_seq,omitempty"`
	WorkingDir    string          `json:"working_dir"`
	Kind          EventKind       `json:"kind"`
	Payload       json.RawMessage `json:"payload"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

type eventJSON Event

func (e Event) MarshalJSON() ([]byte, error) {
	normalized := e.Normalize()
	return json.Marshal(eventJSON(normalized))
}

func (e *Event) UnmarshalJSON(data []byte) error {
	var decoded eventJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*e = Event(decoded).Normalize()
	return nil
}

func (e Event) Normalize() Event {
	if e.SchemaVersion == 0 {
		e.SchemaVersion = CurrentSchemaVersion
	}
	if !e.Ts.IsZero() {
		e.Ts = e.Ts.UTC()
	}
	e.Payload = cloneRawMessage(e.Payload)
	e.Raw = cloneRawMessage(e.Raw)
	return e
}

func (e Event) WithoutRaw() Event {
	e.Raw = nil
	return e
}

func (e Event) Validate() error {
	e = e.Normalize()

	var problems []string
	if e.SchemaVersion != CurrentSchemaVersion {
		problems = append(problems, fmt.Sprintf("schema_version must be %d", CurrentSchemaVersion))
	}
	if strings.TrimSpace(e.EventID) == "" {
		problems = append(problems, "event_id is required")
	}
	if e.Ts.IsZero() {
		problems = append(problems, "ts is required")
	}
	if strings.TrimSpace(e.Harness) == "" {
		problems = append(problems, "harness is required")
	}
	if strings.TrimSpace(e.TranscriptID) == "" {
		problems = append(problems, "transcript_id is required")
	}
	if strings.TrimSpace(e.WorkingDir) == "" {
		problems = append(problems, "working_dir is required")
	}
	if !e.Kind.Valid() {
		problems = append(problems, "kind is invalid")
	}
	if len(e.Payload) == 0 {
		problems = append(problems, "payload is required")
	} else if !json.Valid(e.Payload) {
		problems = append(problems, "payload must be valid JSON")
	}
	if len(e.Raw) > 0 && !json.Valid(e.Raw) {
		problems = append(problems, "raw must be valid JSON when present")
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

type EventOption func(*Event)

func WithEventID(id string) EventOption {
	return func(e *Event) {
		e.EventID = id
	}
}

func WithTimestamp(ts time.Time) EventOption {
	return func(e *Event) {
		e.Ts = ts
	}
}

func WithSourceSeq(seq int64) EventOption {
	return func(e *Event) {
		e.SourceSeq = seq
	}
}

func WithRaw(raw json.RawMessage) EventOption {
	return func(e *Event) {
		e.Raw = cloneRawMessage(raw)
	}
}

func NewEvent(kind EventKind, harness, transcriptID, workingDir string, payload any, opts ...EventOption) (Event, error) {
	payloadJSON, err := MarshalPayload(payload)
	if err != nil {
		return Event{}, err
	}

	e := Event{
		SchemaVersion: CurrentSchemaVersion,
		Ts:            time.Now().UTC(),
		Harness:       harness,
		TranscriptID:  transcriptID,
		WorkingDir:    workingDir,
		Kind:          kind,
		Payload:       payloadJSON,
	}
	for _, opt := range opts {
		opt(&e)
	}
	e = e.Normalize()

	if e.EventID == "" {
		id, err := NewEventID(e.Ts)
		if err != nil {
			return Event{}, err
		}
		e.EventID = id
	}

	if err := e.Validate(); err != nil {
		return Event{}, err
	}
	return e, nil
}

func NewEventID(ts time.Time) (string, error) {
	if ts.IsZero() {
		ts = time.Now()
	}

	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate event id entropy: %w", err)
	}

	return fmt.Sprintf("evt_%016x%016x", uint64(ts.UTC().UnixNano()), binary.BigEndian.Uint64(random[:])), nil
}

func MarshalPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return nil, errors.New("payload is required")
	}

	switch v := payload.(type) {
	case json.RawMessage:
		return validateAndClonePayload(v)
	case []byte:
		return validateAndClonePayload(json.RawMessage(v))
	case string:
		return validateAndClonePayload(json.RawMessage(v))
	default:
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		return validateAndClonePayload(encoded)
	}
}

func DecodePayload[T any](payload json.RawMessage) (T, error) {
	var decoded T
	if len(payload) == 0 {
		return decoded, errors.New("payload is empty")
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return decoded, fmt.Errorf("decode payload: %w", err)
	}
	return decoded, nil
}

const RedactedValue = "<redacted>"

func RedactedJSON() json.RawMessage {
	return json.RawMessage(strconv.Quote(RedactedValue))
}

type TextActor string

const (
	ActorUser      TextActor = "user"
	ActorAssistant TextActor = "assistant"
	ActorSystem    TextActor = "system"
	ActorTool      TextActor = "tool"
	ActorHarness   TextActor = "harness"
)

type TextPayload struct {
	Actor   TextActor `json:"actor"`
	Content string    `json:"content"`
	Turn    int       `json:"turn,omitempty"`
}

type ToolCallPayload struct {
	CorrelationID string          `json:"correlation_id"`
	Tool          string          `json:"tool"`
	Args          json.RawMessage `json:"args,omitempty"`
}

type ToolResultStatus string

const (
	ToolResultOK    ToolResultStatus = "ok"
	ToolResultError ToolResultStatus = "error"
)

type ToolResultPayload struct {
	CorrelationID string           `json:"correlation_id"`
	Status        ToolResultStatus `json:"status"`
	OutputSummary string           `json:"output_summary,omitempty"`
	Error         string           `json:"error,omitempty"`
	Truncated     bool             `json:"truncated,omitempty"`
}

type FileOp string

const (
	FileOpCreate FileOp = "create"
	FileOpModify FileOp = "modify"
	FileOpDelete FileOp = "delete"
	FileOpRename FileOp = "rename"
)

type FileChangePayload struct {
	Path      string `json:"path"`
	Op        FileOp `json:"op"`
	Source    string `json:"source,omitempty"`
	GitCommit string `json:"git_commit,omitempty"`
}

type WorkingDirChangePayload struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type ToolDefPayload struct {
	Name          string          `json:"name"`
	Schema        json.RawMessage `json:"schema"`
	SourceHarness string          `json:"source_harness"`
}

func validateAndClonePayload(payload json.RawMessage) (json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, errors.New("payload is empty")
	}
	if !json.Valid(payload) {
		return nil, errors.New("payload must be valid JSON")
	}
	return cloneRawMessage(payload), nil
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}
