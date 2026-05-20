package journal

import (
	"strings"
	"testing"
	"time"
)

func TestEntryMarkdownRendersDeterministicFrontmatter(t *testing.T) {
	offset := time.FixedZone("example", -7*60*60)
	prev := time.Date(2026, 5, 17, 10, 0, 0, 0, offset)

	entry := Entry{
		Frontmatter: Frontmatter{
			EntryID:              "e_test",
			ThreadID:             "t_test",
			TranscriptID:         "cc-test",
			PrevEntryTS:          &prev,
			Trigger:              "commit:a3f2b1",
			Timestamp:            time.Date(2026, 5, 18, 15, 42, 0, 123456789, offset),
			SourceEventRange:     []string{"evt_first", "evt_last"},
			SourceStartedAt:      time.Date(2026, 5, 18, 14, 10, 0, 0, offset),
			SourceEndedAt:        time.Date(2026, 5, 18, 15, 42, 0, 0, offset),
			SourceTruncated:      false,
			Harness:              "claude-code",
			WorkingDir:           "/workspace/threadmark",
			Branch:               "main",
			FilesTouched:         []string{"internal/core/event_schema.go", `path/with"quote.go`},
			CommitRefs:           []string{"a3f2b1"},
			ReflectorBackend:     "claude-code",
			ReflectorCommandMode: "convenience",
			ReflectorModel:       "claude-opus-4-7",
			ReflectorPromptHash:  "sha256:bd71f3",
			ThreadmarkVersion:    "0.1.0",
		},
		Body: "\n> Core shape landed.\n\nI preserved the event contract.\n",
	}

	markdown, err := entry.Markdown()
	if err != nil {
		t.Fatalf("Markdown returned error: %v", err)
	}

	assertContains(t, markdown, "---\nentry_id: \"e_test\"\nschema_version: 1\n")
	assertContains(t, markdown, "prev_entry_ts: \"2026-05-17T17:00:00Z\"\n")
	assertContains(t, markdown, "timestamp: \"2026-05-18T22:42:00.123456789Z\"\n")
	assertContains(t, markdown, "source_event_range: [\"evt_first\", \"evt_last\"]\n")
	assertContains(t, markdown, `files_touched: ["internal/core/event_schema.go", "path/with\"quote.go"]`+"\n")
	assertContains(t, markdown, "\n---\n\n> Core shape landed.\n\nI preserved the event contract.\n")
}

func TestFrontmatterValidation(t *testing.T) {
	_, err := Entry{Body: "missing frontmatter"}.Markdown()
	if err == nil {
		t.Fatal("Markdown returned nil error")
	}
	if !strings.Contains(err.Error(), "entry_id is required") {
		t.Fatalf("error did not mention missing entry id: %v", err)
	}
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected markdown to contain %q\nactual:\n%s", needle, haystack)
	}
}
