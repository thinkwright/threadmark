package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
)

const Harness = "claude-code"

type HookEventName string

const (
	HookSessionStart     HookEventName = "SessionStart"
	HookUserPromptSubmit HookEventName = "UserPromptSubmit"
	HookPostToolUse      HookEventName = "PostToolUse"
	HookStop             HookEventName = "Stop"
	HookPreCompact       HookEventName = "PreCompact"
	HookPostCompact      HookEventName = "PostCompact"
)

type HookPayload struct {
	SessionID            string          `json:"session_id"`
	TranscriptPath       string          `json:"transcript_path"`
	CWD                  string          `json:"cwd"`
	PermissionMode       string          `json:"permission_mode,omitempty"`
	HookEventName        HookEventName   `json:"hook_event_name"`
	AgentID              string          `json:"agent_id,omitempty"`
	AgentType            string          `json:"agent_type,omitempty"`
	Source               string          `json:"source,omitempty"`
	Model                string          `json:"model,omitempty"`
	Prompt               string          `json:"prompt,omitempty"`
	ToolName             string          `json:"tool_name,omitempty"`
	ToolInput            json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse         json.RawMessage `json:"tool_response,omitempty"`
	ToolUseID            string          `json:"tool_use_id,omitempty"`
	DurationMS           int64           `json:"duration_ms,omitempty"`
	StopHookActive       bool            `json:"stop_hook_active,omitempty"`
	LastAssistantMessage string          `json:"last_assistant_message,omitempty"`
	Trigger              string          `json:"trigger,omitempty"`
}

type Translator struct {
	Now func() time.Time
}

func DecodeHookPayload(data []byte) (HookPayload, error) {
	var payload HookPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return HookPayload{}, fmt.Errorf("decode Claude Code hook payload: %w", err)
	}
	return payload, nil
}

func Translate(payload HookPayload) ([]core.Event, error) {
	return Translator{}.Translate(payload)
}

func (t Translator) Translate(payload HookPayload) ([]core.Event, error) {
	if err := payload.validate(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if t.Now != nil {
		now = t.Now().UTC()
	}

	switch payload.HookEventName {
	case HookSessionStart:
		return t.sessionStart(payload, now)
	case HookUserPromptSubmit:
		return t.userPromptSubmit(payload, now)
	case HookPostToolUse:
		return t.postToolUse(payload, now)
	case HookStop:
		return t.stop(payload, now)
	case HookPreCompact:
		return t.preCompact(payload, now)
	case HookPostCompact:
		return t.postCompact(payload, now)
	default:
		return nil, fmt.Errorf("unsupported Claude Code hook event %q", payload.HookEventName)
	}
}

func (p HookPayload) validate() error {
	var problems []string
	if strings.TrimSpace(p.SessionID) == "" {
		problems = append(problems, "session_id is required")
	}
	if strings.TrimSpace(p.CWD) == "" {
		problems = append(problems, "cwd is required")
	}
	if strings.TrimSpace(string(p.HookEventName)) == "" {
		problems = append(problems, "hook_event_name is required")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (t Translator) sessionStart(payload HookPayload, ts time.Time) ([]core.Event, error) {
	parts := []string{"session_start"}
	if payload.Source != "" {
		parts = append(parts, "source="+payload.Source)
	}
	if payload.Model != "" {
		parts = append(parts, "model="+payload.Model)
	}
	if payload.AgentType != "" {
		parts = append(parts, "agent_type="+payload.AgentType)
	}
	event, err := newTextEvent(payload, ts, core.ActorHarness, strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	return []core.Event{event}, nil
}

func (t Translator) userPromptSubmit(payload HookPayload, ts time.Time) ([]core.Event, error) {
	event, err := newTextEvent(payload, ts, core.ActorUser, payload.Prompt)
	if err != nil {
		return nil, err
	}
	return []core.Event{event}, nil
}

func (t Translator) stop(payload HookPayload, ts time.Time) ([]core.Event, error) {
	events := make([]core.Event, 0, 2)
	if strings.TrimSpace(payload.LastAssistantMessage) != "" {
		event, err := newTextEvent(payload, ts, core.ActorAssistant, payload.LastAssistantMessage)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	event, err := newTextEvent(payload, ts, core.ActorHarness, "stop")
	if err != nil {
		return nil, err
	}
	events = append(events, event)
	return events, nil
}

func (t Translator) preCompact(payload HookPayload, ts time.Time) ([]core.Event, error) {
	cause := strings.ToLower(strings.TrimSpace(payload.Trigger))
	if cause == "" {
		cause = "auto"
	}
	event, err := newTextEvent(payload, ts, core.ActorHarness, "pre_compact cause="+cause)
	if err != nil {
		return nil, err
	}
	return []core.Event{event}, nil
}

func (t Translator) postCompact(payload HookPayload, ts time.Time) ([]core.Event, error) {
	event, err := newTextEvent(payload, ts, core.ActorHarness, "post_compact")
	if err != nil {
		return nil, err
	}
	return []core.Event{event}, nil
}

func (t Translator) postToolUse(payload HookPayload, ts time.Time) ([]core.Event, error) {
	correlationID := payload.ToolUseID
	if correlationID == "" {
		correlationID = "claude-code:" + payload.ToolName
	}

	events := make([]core.Event, 0, 4)
	call, err := core.NewEvent(
		core.KindToolCall,
		Harness,
		payload.SessionID,
		payload.CWD,
		core.ToolCallPayload{
			CorrelationID: correlationID,
			Tool:          payload.ToolName,
			Args:          core.RedactedJSON(),
		},
		core.WithTimestamp(ts),
	)
	if err != nil {
		return nil, err
	}
	events = append(events, call)

	for _, change := range fileChanges(payload) {
		event, err := core.NewEvent(
			core.KindFileChange,
			Harness,
			payload.SessionID,
			payload.CWD,
			change,
			core.WithTimestamp(ts),
		)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	result, err := core.NewEvent(
		core.KindToolResult,
		Harness,
		payload.SessionID,
		payload.CWD,
		core.ToolResultPayload{
			CorrelationID: correlationID,
			Status:        core.ToolResultOK,
			OutputSummary: summarizeToolResponse(payload),
		},
		core.WithTimestamp(ts),
	)
	if err != nil {
		return nil, err
	}
	events = append(events, result)
	return events, nil
}

func newTextEvent(payload HookPayload, ts time.Time, actor core.TextActor, content string) (core.Event, error) {
	return core.NewEvent(
		core.KindText,
		Harness,
		payload.SessionID,
		payload.CWD,
		core.TextPayload{Actor: actor, Content: content},
		core.WithTimestamp(ts),
	)
}

func fileChanges(payload HookPayload) []core.FileChangePayload {
	switch payload.ToolName {
	case "Write":
		return pathChange(payload, core.FileOpCreate)
	case "Edit", "MultiEdit", "NotebookEdit":
		return pathChange(payload, core.FileOpModify)
	case "Bash":
		commit := gitCommitFromPayload(payload)
		if commit == "" {
			return nil
		}
		return []core.FileChangePayload{{
			Op:        core.FileOpModify,
			Source:    "claude-code:Bash",
			GitCommit: commit,
		}}
	default:
		return nil
	}
}

func pathChange(payload HookPayload, op core.FileOp) []core.FileChangePayload {
	path := firstString(payload.ToolInput, "file_path", "filePath", "path")
	if path == "" {
		path = firstString(payload.ToolResponse, "filePath", "file_path", "path")
	}
	if path == "" {
		return nil
	}
	return []core.FileChangePayload{{
		Path:   projectRelativePath(payload.CWD, path),
		Op:     op,
		Source: "claude-code:" + payload.ToolName,
	}}
}

func projectRelativePath(cwd, path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return rel
}

func summarizeToolResponse(payload HookPayload) string {
	parts := []string{"tool=" + payload.ToolName}
	if payload.DurationMS > 0 {
		parts = append(parts, fmt.Sprintf("duration_ms=%d", payload.DurationMS))
	}

	switch payload.ToolName {
	case "Bash":
		command := firstString(payload.ToolInput, "command")
		if command != "" {
			parts = append(parts, "command="+summarizeCommand(command))
		}
		appendByteCounts(&parts, payload.ToolResponse, "stdout", "stderr")
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		path := firstString(payload.ToolInput, "file_path", "filePath", "path")
		if path == "" {
			path = firstString(payload.ToolResponse, "filePath", "file_path", "path")
		}
		if path != "" {
			parts = append(parts, "path="+projectRelativePath(payload.CWD, path))
		}
	default:
		keys := objectKeys(payload.ToolResponse)
		if len(keys) > 0 {
			parts = append(parts, "response_keys="+strings.Join(keys, ","))
		} else if len(payload.ToolResponse) > 0 {
			parts = append(parts, fmt.Sprintf("response_bytes=%d", len(payload.ToolResponse)))
		}
	}

	return strings.Join(parts, " ")
}

func summarizeCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > 4 {
		fields = fields[:4]
	}
	return strings.Join(fields, " ")
}

func appendByteCounts(parts *[]string, raw json.RawMessage, keys ...string) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return
	}
	for _, key := range keys {
		value, ok := obj[key].(string)
		if ok {
			*parts = append(*parts, fmt.Sprintf("%s_bytes=%d", key, len(value)))
		}
	}
}

func objectKeys(raw json.RawMessage) []string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstString(raw json.RawMessage, keys ...string) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var gitCommitHashPattern = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

func gitCommitFromPayload(payload HookPayload) string {
	command := firstString(payload.ToolInput, "command")
	if !looksLikeGitCommit(command) {
		return ""
	}
	for _, field := range []string{"stdout", "stderr"} {
		if hash := gitCommitHashPattern.FindString(firstString(payload.ToolResponse, field)); hash != "" {
			return hash
		}
	}
	return ""
}

func looksLikeGitCommit(command string) bool {
	fields := strings.Fields(command)
	for idx := 0; idx < len(fields)-1; idx++ {
		if fields[idx] == "git" && fields[idx+1] == "commit" {
			return true
		}
	}
	return false
}
