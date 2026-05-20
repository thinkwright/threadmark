package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreAppendAndLastEntries(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(t.TempDir(), ".threadmark")
	store := NewStore(root)

	first := testEntry("e_first", "evt_1", "evt_2", "first body")
	second := testEntry("e_second", "evt_3", "evt_4", "second body")

	path, err := store.Append(workingDir, first)
	if err != nil {
		t.Fatalf("Append first returned error: %v", err)
	}
	if _, err := store.Append(workingDir, second); err != nil {
		t.Fatalf("Append second returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if got, want := info.Mode().Perm(), FileMode; got != want {
		t.Fatalf("journal mode = %v, want %v", got, want)
	}

	entries, err := store.LastEntries(workingDir, 1)
	if err != nil {
		t.Fatalf("LastEntries returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if !contains(entries[0], "entry_id: \"e_second\"") {
		t.Fatalf("last entry did not contain second entry:\n%s", entries[0])
	}
	if contains(entries[0], "entry_id: \"e_first\"") {
		t.Fatalf("last entry unexpectedly contained first entry:\n%s", entries[0])
	}
}

func TestSplitEntriesIgnoresBodyHorizontalRule(t *testing.T) {
	first, err := testEntry("e_first", "evt_1", "evt_2", "before\n\n---\nentry_id: \"not-an-entry\"\n\nafter").Markdown()
	if err != nil {
		t.Fatalf("first Markdown returned error: %v", err)
	}
	second, err := testEntry("e_second", "evt_3", "evt_4", "second body").Markdown()
	if err != nil {
		t.Fatalf("second Markdown returned error: %v", err)
	}

	entries := SplitEntries([]byte(first + "\n\n" + second))
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2:\n%#v", len(entries), entries)
	}
	if !contains(entries[0], "before\n\n---\nentry_id: \"not-an-entry\"\n\nafter") {
		t.Fatalf("first entry lost body horizontal rule:\n%s", entries[0])
	}
	if !contains(entries[1], "entry_id: \"e_second\"") {
		t.Fatalf("second entry missing expected frontmatter:\n%s", entries[1])
	}
}

func TestStoreAppendConcurrent(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(t.TempDir(), ".threadmark")
	store := NewStore(root)

	const entryCount = 32
	var wg sync.WaitGroup
	errs := make(chan error, entryCount)
	for i := 0; i < entryCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry := testEntry(
				fmt.Sprintf("e_%02d", i),
				fmt.Sprintf("evt_%02d_first", i),
				fmt.Sprintf("evt_%02d_last", i),
				fmt.Sprintf("body %02d", i),
			)
			if _, err := store.Append(workingDir, entry); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Append returned error: %v", err)
		}
	}

	entries, err := store.LastEntries(workingDir, entryCount)
	if err != nil {
		t.Fatalf("LastEntries returned error: %v", err)
	}
	if len(entries) != entryCount {
		t.Fatalf("len(entries) = %d, want %d", len(entries), entryCount)
	}
}

func TestProjectHashResolvesSymlinks(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	realHash, realResolved, err := ProjectHash(realDir)
	if err != nil {
		t.Fatalf("ProjectHash real dir returned error: %v", err)
	}
	linkHash, linkResolved, err := ProjectHash(link)
	if err != nil {
		t.Fatalf("ProjectHash symlink returned error: %v", err)
	}
	if realHash != linkHash {
		t.Fatalf("hashes differ: real %s, symlink %s", realHash, linkHash)
	}
	if realResolved != linkResolved {
		t.Fatalf("resolved paths differ: real %s, symlink %s", realResolved, linkResolved)
	}
	if len(realHash) != ProjectHashLength {
		t.Fatalf("project hash length = %d, want %d", len(realHash), ProjectHashLength)
	}
}

func TestStoreProjectIDUsesExistingLegacyDirectoryWhenStateMatches(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(t.TempDir(), ".threadmark")
	store := NewStore(root)

	canonicalHash, resolvedWorkingDir, err := ProjectHash(workingDir)
	if err != nil {
		t.Fatalf("ProjectHash returned error: %v", err)
	}
	legacyHash := canonicalHash[:LegacyProjectHashLength]
	legacyDir := filepath.Join(root, "projects", legacyHash)
	if err := os.MkdirAll(legacyDir, DirMode); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	state := fmt.Sprintf("{\"working_dir\":%q}\n", resolvedWorkingDir)
	if err := os.WriteFile(filepath.Join(legacyDir, "state.json"), []byte(state), FileMode); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	projectID, resolved, err := store.ProjectID(workingDir)
	if err != nil {
		t.Fatalf("ProjectID returned error: %v", err)
	}
	if projectID != legacyHash {
		t.Fatalf("project id = %q, want legacy %q", projectID, legacyHash)
	}
	if resolved != resolvedWorkingDir {
		t.Fatalf("resolved working dir = %q, want %q", resolved, resolvedWorkingDir)
	}

	projectDir, _, err := store.ProjectDir(workingDir)
	if err != nil {
		t.Fatalf("ProjectDir returned error: %v", err)
	}
	if projectDir != legacyDir {
		t.Fatalf("project dir = %q, want %q", projectDir, legacyDir)
	}
}

func TestStoreProjectIDIgnoresLegacyDirectoryWhenStateDiffers(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(t.TempDir(), ".threadmark")
	store := NewStore(root)

	canonicalHash, _, err := ProjectHash(workingDir)
	if err != nil {
		t.Fatalf("ProjectHash returned error: %v", err)
	}
	legacyHash := canonicalHash[:LegacyProjectHashLength]
	legacyDir := filepath.Join(root, "projects", legacyHash)
	if err := os.MkdirAll(legacyDir, DirMode); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "state.json"), []byte("{\"working_dir\":\"/tmp/other\"}\n"), FileMode); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	projectID, _, err := store.ProjectID(workingDir)
	if err != nil {
		t.Fatalf("ProjectID returned error: %v", err)
	}
	if projectID != canonicalHash {
		t.Fatalf("project id = %q, want canonical %q", projectID, canonicalHash)
	}
}

func testEntry(entryID, firstEventID, lastEventID, body string) Entry {
	ts := time.Date(2026, 5, 18, 15, 42, 0, 0, time.UTC)
	return Entry{
		Frontmatter: Frontmatter{
			EntryID:              entryID,
			ThreadID:             "t_test",
			TranscriptID:         "cc-test",
			Trigger:              "manual",
			Timestamp:            ts,
			SourceEventRange:     []string{firstEventID, lastEventID},
			SourceStartedAt:      ts.Add(-time.Hour),
			SourceEndedAt:        ts,
			Harness:              "claude-code",
			WorkingDir:           "/workspace/threadmark",
			ReflectorBackend:     "claude-code",
			ReflectorCommandMode: "convenience",
			ReflectorModel:       "claude-opus-4-7",
			ReflectorPromptHash:  "sha256:test",
			ThreadmarkVersion:    "0.1.0",
		},
		Body: body,
	}
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
}
