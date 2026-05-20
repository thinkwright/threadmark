package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

func initCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: threadmark init <claude-code|codex> [options]")
		return 2
	}
	switch args[0] {
	case "claude-code", "claudecode":
		return initClaudeCode(args[1:], stdout, stderr)
	case "codex":
		return initCodex(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "threadmark: unknown init target %q\n", args[0])
		return 2
	}
}

func initClaudeCode(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init claude-code", flag.ContinueOnError)
	flags.SetOutput(stderr)
	scope := flags.String("scope", "project", "settings scope: project or user")
	settingsFile := flags.String("settings-file", "", "Claude Code settings file to update")
	hookScript := flags.String("hook-script", "", "hook script path (default adapters/claudecode/hooks/threadmark-hook.sh when available)")
	printOnly := flags.Bool("print", false, "print settings fragment instead of writing")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	command, commandArgs, err := resolveHookCommand(*hookScript)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark init: %v\n", err)
		return 1
	}
	fragment := claudeCodeSettingsFragment(command, commandArgs)
	if *printOnly {
		return writeJSON(stdout, fragment)
	}

	path, err := resolveClaudeSettingsPath(*scope, *settingsFile)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark init: %v\n", err)
		return 1
	}
	if err := mergeClaudeSettings(path, fragment, command, commandArgs); err != nil {
		fmt.Fprintf(stderr, "threadmark init: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "threadmark: installed Claude Code hooks in %s\n", path)
	return 0
}

func initCodex(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init codex", flag.ContinueOnError)
	flags.SetOutput(stderr)
	scope := flags.String("scope", "project", "hooks scope: project or user")
	hooksFile := flags.String("hooks-file", "", "Codex hooks file to update")
	hookScript := flags.String("hook-script", "", "hook script path (default adapters/codex/hooks/threadmark-hook.sh when available)")
	printOnly := flags.Bool("print", false, "print hooks fragment instead of writing")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	command, err := resolveCodexHookCommand(*hookScript)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark init: %v\n", err)
		return 1
	}
	fragment := codexHooksFragment(command)
	if *printOnly {
		return writeJSON(stdout, fragment)
	}

	path, err := resolveCodexHooksPath(*scope, *hooksFile)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark init: %v\n", err)
		return 1
	}
	if err := mergeCodexHooks(path, fragment, command); err != nil {
		fmt.Fprintf(stderr, "threadmark init: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "threadmark: installed Codex hooks in %s\n", path)
	fmt.Fprintln(stderr, "threadmark: run /hooks in Codex once to review and trust these project hooks")
	return 0
}

func resolveHookCommand(hookScript string) (string, []string, error) {
	if strings.TrimSpace(hookScript) != "" {
		abs, err := filepath.Abs(hookScript)
		if err != nil {
			return "", nil, err
		}
		return abs, nil, nil
	}

	candidate := filepath.Join("adapters", "claudecode", "hooks", "threadmark-hook.sh")
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return "", nil, err
		}
		return abs, nil, nil
	}
	return "threadmark", []string{"hook", "claude-code"}, nil
}

func resolveCodexHookCommand(hookScript string) (string, error) {
	if strings.TrimSpace(hookScript) != "" {
		abs, err := filepath.Abs(hookScript)
		if err != nil {
			return "", err
		}
		return abs, nil
	}

	candidate := filepath.Join("adapters", "codex", "hooks", "threadmark-hook.sh")
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	return "threadmark hook codex", nil
}

func resolveClaudeSettingsPath(scope, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	switch scope {
	case "project":
		return filepath.Join(".claude", "settings.json"), nil
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find user home: %w", err)
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	default:
		return "", fmt.Errorf("unknown scope %q", scope)
	}
}

func resolveCodexHooksPath(scope, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	switch scope {
	case "project":
		return filepath.Join(".codex", "hooks.json"), nil
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find user home: %w", err)
		}
		return filepath.Join(home, ".codex", "hooks.json"), nil
	default:
		return "", fmt.Errorf("unknown scope %q", scope)
	}
}

func claudeCodeSettingsFragment(command string, args []string) map[string]any {
	handler := map[string]any{
		"type":    "command",
		"command": command,
		"timeout": float64(5),
	}
	if len(args) > 0 {
		handler["args"] = append([]string(nil), args...)
	} else {
		handler["args"] = []string{}
	}

	hookGroup := func(matcher string) map[string]any {
		group := map[string]any{
			"hooks": []any{cloneMap(handler)},
		}
		if matcher != "" {
			group["matcher"] = matcher
		}
		return group
	}

	return map[string]any{
		"hooks": map[string]any{
			"SessionStart":     []any{hookGroup("startup|resume|clear|compact")},
			"UserPromptSubmit": []any{hookGroup("")},
			"PostToolUse":      []any{hookGroup("*")},
			"Stop":             []any{hookGroup("")},
			"PreCompact":       []any{hookGroup("")},
			"PostCompact":      []any{hookGroup("")},
		},
	}
}

func codexHooksFragment(command string) map[string]any {
	handler := map[string]any{
		"type":    "command",
		"command": command,
		"timeout": float64(5),
	}

	hookGroup := func(matcher string) map[string]any {
		group := map[string]any{
			"hooks": []any{cloneMap(handler)},
		}
		if matcher != "" {
			group["matcher"] = matcher
		}
		return group
	}

	return map[string]any{
		"hooks": map[string]any{
			"SessionStart":     []any{hookGroup("startup|resume|clear")},
			"UserPromptSubmit": []any{hookGroup("")},
			"PostToolUse":      []any{hookGroup("*")},
			"Stop":             []any{hookGroup("")},
			"PreCompact":       []any{hookGroup("")},
			"PostCompact":      []any{hookGroup("")},
		},
	}
}

func mergeClaudeSettings(path string, fragment map[string]any, command string, args []string) error {
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("decode Claude settings: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read Claude settings: %w", err)
	}

	settingsHooks := mapValue(settings, "hooks")
	fragmentHooks := mapValue(fragment, "hooks")
	for eventName, value := range fragmentHooks {
		existing := arrayValue(settingsHooks, eventName)
		existing = removeHookGroupsForCommand(existing, command, args)
		settingsHooks[eventName] = append(existing, arrayFromAny(value)...)
	}
	settings["hooks"] = settingsHooks

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	tmp := path + ".tmp"
	data, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Claude settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write settings temp file: %w", err)
	}
	return os.Rename(tmp, path)
}

func mergeCodexHooks(path string, fragment map[string]any, command string) error {
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("decode Codex hooks: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read Codex hooks: %w", err)
	}

	settingsHooks := mapValue(settings, "hooks")
	fragmentHooks := mapValue(fragment, "hooks")
	for eventName, value := range fragmentHooks {
		existing := arrayValue(settingsHooks, eventName)
		existing = removeHookGroupsForCommand(existing, command, nil)
		settingsHooks[eventName] = append(existing, arrayFromAny(value)...)
	}
	settings["hooks"] = settingsHooks

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create hooks directory: %w", err)
	}
	tmp := path + ".tmp"
	data, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Codex hooks: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write hooks temp file: %w", err)
	}
	return os.Rename(tmp, path)
}

func removeHookGroupsForCommand(groups []any, command string, args []string) []any {
	args = normalizeArgs(args)
	filtered := make([]any, 0, len(groups))
	for _, group := range groups {
		groupMap, ok := group.(map[string]any)
		if !ok {
			filtered = append(filtered, group)
			continue
		}
		hooks := arrayFromAny(groupMap["hooks"])
		if len(hooks) == 0 {
			filtered = append(filtered, group)
			continue
		}

		keptHooks := make([]any, 0, len(hooks))
		for _, hook := range hooks {
			if hookUsesCommand(hook, command, args) {
				continue
			}
			keptHooks = append(keptHooks, hook)
		}
		if len(keptHooks) == len(hooks) {
			filtered = append(filtered, group)
			continue
		}
		if len(keptHooks) == 0 {
			continue
		}

		updatedGroup := cloneMap(groupMap)
		updatedGroup["hooks"] = keptHooks
		filtered = append(filtered, updatedGroup)
	}
	return filtered
}

func hookUsesCommand(hook any, command string, args []string) bool {
	hookMap, ok := hook.(map[string]any)
	if !ok {
		return false
	}
	if hookMap["command"] != command {
		return false
	}
	return reflect.DeepEqual(normalizeArgs(stringSliceValue(hookMap["args"])), normalizeArgs(args))
}

func normalizeArgs(args []string) []string {
	if args == nil {
		return []string{}
	}
	return args
}

func mapValue(parent map[string]any, key string) map[string]any {
	if value, ok := parent[key].(map[string]any); ok {
		return value
	}
	m := map[string]any{}
	parent[key] = m
	return m
}

func arrayValue(parent map[string]any, key string) []any {
	return arrayFromAny(parent[key])
}

func arrayFromAny(value any) []any {
	switch typed := value.(type) {
	case []any:
		return append([]any(nil), typed...)
	default:
		return nil
	}
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil
			}
			result = append(result, text)
		}
		return result
	default:
		return nil
	}
}

func cloneMap(m map[string]any) map[string]any {
	clone := make(map[string]any, len(m))
	for key, value := range m {
		clone[key] = value
	}
	return clone
}

func writeJSON(output io.Writer, value any) int {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return 1
	}
	return 0
}
