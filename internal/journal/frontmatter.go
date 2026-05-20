package journal

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const CurrentSchemaVersion = 1

type Frontmatter struct {
	EntryID              string
	SchemaVersion        int
	ThreadID             string
	TranscriptID         string
	PrevEntryTS          *time.Time
	Trigger              string
	Timestamp            time.Time
	SourceEventRange     []string
	SourceStartedAt      time.Time
	SourceEndedAt        time.Time
	SourceTruncated      bool
	Harness              string
	WorkingDir           string
	Branch               string
	FilesTouched         []string
	CommitRefs           []string
	ReflectorBackend     string
	ReflectorCommandMode string
	ReflectorModel       string
	ReflectorPromptHash  string
	ThreadmarkVersion    string
}

type Entry struct {
	Frontmatter Frontmatter
	Body        string
}

func NewEntryID(ts time.Time) (string, error) {
	if ts.IsZero() {
		ts = time.Now()
	}

	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate entry id entropy: %w", err)
	}

	return fmt.Sprintf("e_%016x%08x", uint64(ts.UTC().UnixNano()), binary.BigEndian.Uint32(random[:])), nil
}

func (f Frontmatter) Normalize() Frontmatter {
	if f.SchemaVersion == 0 {
		f.SchemaVersion = CurrentSchemaVersion
	}
	f.Timestamp = normalizeTime(f.Timestamp)
	f.SourceStartedAt = normalizeTime(f.SourceStartedAt)
	f.SourceEndedAt = normalizeTime(f.SourceEndedAt)
	if f.PrevEntryTS != nil {
		prev := normalizeTime(*f.PrevEntryTS)
		f.PrevEntryTS = &prev
	}
	return f
}

func (f Frontmatter) Validate() error {
	f = f.Normalize()

	var problems []string
	if f.EntryID == "" {
		problems = append(problems, "entry_id is required")
	}
	if f.SchemaVersion != CurrentSchemaVersion {
		problems = append(problems, fmt.Sprintf("schema_version must be %d", CurrentSchemaVersion))
	}
	if f.ThreadID == "" {
		problems = append(problems, "thread_id is required")
	}
	if f.TranscriptID == "" {
		problems = append(problems, "transcript_id is required")
	}
	if f.Trigger == "" {
		problems = append(problems, "trigger is required")
	}
	if f.Timestamp.IsZero() {
		problems = append(problems, "timestamp is required")
	}
	if len(f.SourceEventRange) != 2 {
		problems = append(problems, "source_event_range must contain first and last event ids")
	}
	if f.SourceStartedAt.IsZero() {
		problems = append(problems, "source_started_at is required")
	}
	if f.SourceEndedAt.IsZero() {
		problems = append(problems, "source_ended_at is required")
	}
	if f.Harness == "" {
		problems = append(problems, "harness is required")
	}
	if f.WorkingDir == "" {
		problems = append(problems, "working_dir is required")
	}
	if f.ReflectorBackend == "" {
		problems = append(problems, "reflector_backend is required")
	}
	if f.ReflectorCommandMode == "" {
		problems = append(problems, "reflector_command_mode is required")
	}
	if f.ReflectorModel == "" {
		problems = append(problems, "reflector_model is required")
	}
	if f.ReflectorPromptHash == "" {
		problems = append(problems, "reflector_prompt_hash is required")
	}
	if f.ThreadmarkVersion == "" {
		problems = append(problems, "threadmark_version is required")
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (f Frontmatter) Markdown() (string, error) {
	f = f.Normalize()
	if err := f.Validate(); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("---\n")
	writeString(&b, "entry_id", f.EntryID)
	writeInt(&b, "schema_version", f.SchemaVersion)
	writeString(&b, "thread_id", f.ThreadID)
	writeString(&b, "transcript_id", f.TranscriptID)
	if f.PrevEntryTS != nil {
		writeTime(&b, "prev_entry_ts", *f.PrevEntryTS)
	}
	b.WriteString("\n")

	writeString(&b, "trigger", f.Trigger)
	writeTime(&b, "timestamp", f.Timestamp)
	b.WriteString("\n")

	writeStringSlice(&b, "source_event_range", f.SourceEventRange)
	writeTime(&b, "source_started_at", f.SourceStartedAt)
	writeTime(&b, "source_ended_at", f.SourceEndedAt)
	writeBool(&b, "source_truncated", f.SourceTruncated)
	b.WriteString("\n")

	writeString(&b, "harness", f.Harness)
	writeString(&b, "working_dir", f.WorkingDir)
	if f.Branch != "" {
		writeString(&b, "branch", f.Branch)
	}
	writeStringSlice(&b, "files_touched", f.FilesTouched)
	writeStringSlice(&b, "commit_refs", f.CommitRefs)
	b.WriteString("\n")

	writeString(&b, "reflector_backend", f.ReflectorBackend)
	writeString(&b, "reflector_command_mode", f.ReflectorCommandMode)
	writeString(&b, "reflector_model", f.ReflectorModel)
	writeString(&b, "reflector_prompt_hash", f.ReflectorPromptHash)
	writeString(&b, "threadmark_version", f.ThreadmarkVersion)
	b.WriteString("---\n")

	return b.String(), nil
}

func (e Entry) Markdown() (string, error) {
	frontmatter, err := e.Frontmatter.Markdown()
	if err != nil {
		return "", err
	}

	body := strings.TrimSpace(e.Body)
	if body == "" {
		return "", errors.New("body is required")
	}

	return frontmatter + "\n" + body + "\n", nil
}

func normalizeTime(ts time.Time) time.Time {
	if ts.IsZero() {
		return ts
	}
	return ts.UTC()
}

func writeString(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(quote(value))
	b.WriteByte('\n')
}

func writeInt(b *strings.Builder, key string, value int) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(fmt.Sprintf("%d", value))
	b.WriteByte('\n')
}

func writeBool(b *strings.Builder, key string, value bool) {
	b.WriteString(key)
	b.WriteString(": ")
	if value {
		b.WriteString("true\n")
	} else {
		b.WriteString("false\n")
	}
}

func writeTime(b *strings.Builder, key string, value time.Time) {
	writeString(b, key, value.UTC().Format(time.RFC3339Nano))
}

func writeStringSlice(b *strings.Builder, key string, values []string) {
	b.WriteString(key)
	b.WriteString(": [")
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quote(value))
	}
	b.WriteString("]\n")
}

func quote(value string) string {
	return strconv.Quote(value)
}
