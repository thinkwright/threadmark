package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	DirMode                 os.FileMode = 0o700
	FileMode                os.FileMode = 0o600
	ProjectHashLength                   = 16
	LegacyProjectHashLength             = 8
)

type Store struct {
	Root string
}

var journalPathLocks sync.Map

func NewStore(root string) Store {
	return Store{Root: root}
}

func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}
	return filepath.Join(home, ".threadmark"), nil
}

func ProjectHash(workingDir string) (string, string, error) {
	resolved, err := resolveWorkingDir(workingDir)
	if err != nil {
		return "", "", err
	}

	sum := sha256.Sum256([]byte(resolved))
	return hex.EncodeToString(sum[:])[:ProjectHashLength], resolved, nil
}

func (s Store) ProjectID(workingDir string) (string, string, error) {
	hash, resolved, err := ProjectHash(workingDir)
	if err != nil {
		return "", "", err
	}
	root, err := s.root()
	if err != nil {
		return "", "", err
	}
	projectsDir := filepath.Join(root, "projects")
	canonicalDir := filepath.Join(projectsDir, hash)
	if projectDirMatchesWorkingDir(canonicalDir, resolved) {
		return hash, resolved, nil
	}

	legacyHash := hash[:LegacyProjectHashLength]
	legacyDir := filepath.Join(projectsDir, legacyHash)
	if projectDirMatchesWorkingDir(legacyDir, resolved) {
		return legacyHash, resolved, nil
	}
	return hash, resolved, nil
}

func (s Store) ProjectDir(workingDir string) (string, string, error) {
	hash, resolved, err := s.ProjectID(workingDir)
	if err != nil {
		return "", "", err
	}
	root, err := s.root()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(root, "projects", hash), resolved, nil
}

func projectDirMatchesWorkingDir(projectDir, resolvedWorkingDir string) bool {
	data, err := os.ReadFile(filepath.Join(projectDir, "state.json"))
	if err != nil {
		return false
	}
	var state struct {
		WorkingDir string `json:"working_dir"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return false
	}
	return state.WorkingDir == resolvedWorkingDir
}

func (s Store) JournalPath(workingDir string) (string, string, error) {
	projectDir, resolved, err := s.ProjectDir(workingDir)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(projectDir, "journal.md"), resolved, nil
}

func (s Store) StatePath(workingDir string) (string, string, error) {
	projectDir, resolved, err := s.ProjectDir(workingDir)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(projectDir, "state.json"), resolved, nil
}

func (s Store) EnsureProject(workingDir string) (string, string, error) {
	projectDir, resolved, err := s.ProjectDir(workingDir)
	if err != nil {
		return "", "", err
	}
	if err := s.ensureProjectDirs(projectDir); err != nil {
		return "", "", err
	}
	return projectDir, resolved, nil
}

func (s Store) Append(workingDir string, entry Entry) (string, error) {
	path, _, err := s.JournalPath(workingDir)
	if err != nil {
		return "", err
	}

	markdown, err := entry.Markdown()
	if err != nil {
		return "", err
	}
	if err := s.ensureProjectDirs(filepath.Dir(path)); err != nil {
		return "", err
	}

	lock := journalPathLock(path)
	lock.Lock()
	defer lock.Unlock()

	var prefix []byte
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		prefix = []byte("\n\n")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat journal: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, FileMode)
	if err != nil {
		return "", fmt.Errorf("open journal: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(append(prefix, []byte(markdown)...)); err != nil {
		return "", fmt.Errorf("write journal entry: %w", err)
	}
	if err := file.Chmod(FileMode); err != nil {
		return "", fmt.Errorf("set journal permissions: %w", err)
	}

	return path, nil
}

func (s Store) LastEntries(workingDir string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}

	path, _, err := s.JournalPath(workingDir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}

	entries := SplitEntries(data)
	if len(entries) <= n {
		return entries, nil
	}
	return entries[len(entries)-n:], nil
}

func SplitEntries(data []byte) []string {
	text := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if text == "" {
		return nil
	}

	starts := entryStartOffsets(text)
	entries := make([]string, 0, len(starts))
	for i, start := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		entry := strings.TrimSpace(text[start:end])
		if entry == "" {
			continue
		}
		entries = append(entries, entry+"\n")
	}
	return entries
}

func journalPathLock(path string) *sync.Mutex {
	value, _ := journalPathLocks.LoadOrStore(path, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func entryStartOffsets(text string) []int {
	var starts []int
	for offset := 0; offset < len(text); {
		lineEnd := strings.IndexByte(text[offset:], '\n')
		nextOffset := len(text)
		if lineEnd >= 0 {
			lineEnd += offset
			nextOffset = lineEnd + 1
		} else {
			lineEnd = len(text)
		}

		line := strings.TrimSuffix(text[offset:lineEnd], "\r")
		if line == "---" && looksLikeEntryFrontmatter(text, nextOffset) {
			starts = append(starts, offset)
		}

		if nextOffset == len(text) {
			break
		}
		offset = nextOffset
	}
	return starts
}

func looksLikeEntryFrontmatter(text string, offset int) bool {
	if offset >= len(text) {
		return false
	}

	required := map[string]bool{
		"entry_id:":       false,
		"schema_version:": false,
		"thread_id:":      false,
		"transcript_id:":  false,
	}
	for offset < len(text) {
		lineEnd := strings.IndexByte(text[offset:], '\n')
		nextOffset := len(text)
		if lineEnd >= 0 {
			lineEnd += offset
			nextOffset = lineEnd + 1
		} else {
			lineEnd = len(text)
		}

		line := strings.TrimSpace(strings.TrimSuffix(text[offset:lineEnd], "\r"))
		if line == "---" {
			for _, seen := range required {
				if !seen {
					return false
				}
			}
			return true
		}
		for key := range required {
			if strings.HasPrefix(line, key) {
				required[key] = true
			}
		}
		if nextOffset == len(text) {
			break
		}
		offset = nextOffset
	}
	return false
}

func ensurePrivateDir(path string) error {
	if path == "" || path == "." || path == string(filepath.Separator) {
		return fmt.Errorf("refusing to create unsafe private directory %q", path)
	}
	if err := os.MkdirAll(path, DirMode); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	if err := os.Chmod(path, DirMode); err != nil {
		return fmt.Errorf("set private directory permissions: %w", err)
	}
	return nil
}

func resolveWorkingDir(workingDir string) (string, error) {
	if strings.TrimSpace(workingDir) == "" {
		return "", errors.New("working_dir is required")
	}

	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return "", fmt.Errorf("resolve absolute working dir: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve working dir symlinks: %w", err)
	}
	return resolved, nil
}

func (s Store) root() (string, error) {
	if strings.TrimSpace(s.Root) != "" {
		return s.Root, nil
	}
	return DefaultRoot()
}

func (s Store) ensureProjectDirs(projectDir string) error {
	root, err := s.root()
	if err != nil {
		return err
	}
	for _, dir := range []string{root, filepath.Join(root, "projects"), projectDir} {
		if err := ensurePrivateDir(dir); err != nil {
			return err
		}
	}
	return nil
}
