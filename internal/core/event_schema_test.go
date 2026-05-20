package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEventJSONNormalizesTimestampToUTC(t *testing.T) {
	offset := time.FixedZone("example", -7*60*60)
	ts := time.Date(2026, 5, 18, 12, 34, 56, 123456789, offset)

	event, err := NewEvent(
		KindText,
		"codex",
		"session-1",
		"/workspace/threadmark",
		TextPayload{Actor: ActorUser, Content: "continue"},
		WithEventID("evt_test"),
		WithTimestamp(ts),
	)
	if err != nil {
		t.Fatalf("NewEvent returned error: %v", err)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(encoded), `"ts":"2026-05-18T19:34:56.123456789Z"`) {
		t.Fatalf("timestamp was not encoded as UTC RFC3339Nano: %s", encoded)
	}

	var decoded Event
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got, want := decoded.Ts.Format(time.RFC3339Nano), "2026-05-18T19:34:56.123456789Z"; got != want {
		t.Fatalf("decoded timestamp = %s, want %s", got, want)
	}
}

func TestEventDecodeIgnoresUnknownFields(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"event_id": "evt_unknown_fields",
		"ts": "2026-05-18T19:34:56.123456789-07:00",
		"harness": "claude-code",
		"transcript_id": "cc-1",
		"source_seq": 7,
		"working_dir": "/workspace/threadmark",
		"kind": "tool_result",
		"payload": {"correlation_id": "tool-1", "status": "ok"},
		"future_field": {"ignored": true}
	}`)

	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if got, want := event.Ts.Format(time.RFC3339Nano), "2026-05-19T02:34:56.123456789Z"; got != want {
		t.Fatalf("timestamp = %s, want %s", got, want)
	}
}

func TestTypedPayloadRoundTrip(t *testing.T) {
	args := json.RawMessage(`{"cmd":"go test ./..."}`)
	payload, err := MarshalPayload(ToolCallPayload{
		CorrelationID: "call-1",
		Tool:          "exec_command",
		Args:          args,
	})
	if err != nil {
		t.Fatalf("MarshalPayload returned error: %v", err)
	}

	decoded, err := DecodePayload[ToolCallPayload](payload)
	if err != nil {
		t.Fatalf("DecodePayload returned error: %v", err)
	}
	if decoded.CorrelationID != "call-1" {
		t.Fatalf("correlation id = %q, want call-1", decoded.CorrelationID)
	}
	if string(decoded.Args) != string(args) {
		t.Fatalf("args = %s, want %s", decoded.Args, args)
	}
}

func TestValidateRejectsUnknownKindAndInvalidPayload(t *testing.T) {
	event := Event{
		SchemaVersion: CurrentSchemaVersion,
		EventID:       "evt_bad",
		Ts:            time.Now(),
		Harness:       "codex",
		TranscriptID:  "session-1",
		WorkingDir:    "/tmp/project",
		Kind:          EventKind("surprise"),
		Payload:       json.RawMessage(`{bad json`),
	}

	err := event.Validate()
	if err == nil {
		t.Fatal("Validate returned nil error")
	}
	if !strings.Contains(err.Error(), "kind is invalid") {
		t.Fatalf("Validate error does not mention invalid kind: %v", err)
	}
	if !strings.Contains(err.Error(), "payload must be valid JSON") {
		t.Fatalf("Validate error does not mention invalid payload: %v", err)
	}
}

func TestRedactedJSON(t *testing.T) {
	if got, want := string(RedactedJSON()), `"<redacted>"`; got != want {
		t.Fatalf("RedactedJSON = %s, want %s", got, want)
	}
}
