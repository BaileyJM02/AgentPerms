// agentperms compiles a single permissions.yaml into the native permission
// formats of Claude Code, Zed's native agent, and the Cursor CLI, splicing
// each into its live config file without touching anything else in them.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agentperms:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "", "path to permissions.yaml (default: ./permissions.yaml, then ~/.config/agentperms/permissions.yaml)")
	write := flag.Bool("write", false, "patch the target files in place (default: print previews)")
	targetsFlag := flag.String("targets", "claude,zed,cursor,cursor-ide", "comma-separated subset of targets to generate")
	cursorReplace := flag.Bool("cursor-replace", false, "replace cursor allow/deny lists instead of union-merging with existing entries")
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path, err := resolveConfigPath(*cfgPath, home)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}

	var targets []string
	for _, t := range strings.Split(*targetsFlag, ",") {
		t = strings.TrimSpace(t)
		switch t {
		case targetClaude, targetZed, targetCursor, targetCursorIDE:
			targets = append(targets, t)
		case "":
		default:
			return fmt.Errorf("unknown target %q (want claude|zed|cursor)", t)
		}
	}

	gen, err := generate(cfg, home)
	if err != nil {
		return err
	}
	for _, w := range gen.warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	for _, t := range targets {
		outPath := outputFor(cfg, t, home)
		src, err := os.ReadFile(outPath)
		if err != nil {
			if t == targetCursorIDE && os.IsNotExist(err) {
				src = []byte("{}")
			} else {
				return fmt.Errorf("%s: %w", t, err)
			}
		}
		key, val, err := buildValue(t, gen, src, *cursorReplace)
		if err != nil {
			return fmt.Errorf("%s (%s): %w", t, outPath, err)
		}
		if !*write {
			fmt.Printf("== %s → %s (key %q)\n", t, outPath, key)
			preview, err := marshalPretty(val)
			if err != nil {
				return err
			}
			fmt.Println(preview)
			continue
		}
		var out []byte
		if key == "" { // whole-file target (cursor-ide): merge into the root object
			pretty, err := marshalPretty(val)
			if err != nil {
				return fmt.Errorf("%s (%s): %w", t, outPath, err)
			}
			out = []byte(pretty + "\n")
		} else {
			out, err = spliceKey(src, key, val)
			if err != nil {
				return fmt.Errorf("%s (%s): %w", t, outPath, err)
			}
		}
		if t != targetZed && !json.Valid(out) {
			return fmt.Errorf("%s: refusing to write %s — result is not valid JSON", t, outPath)
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(outPath); err == nil {
			mode = info.Mode().Perm()
			if err := os.WriteFile(outPath+".bak", src, mode); err != nil {
				return fmt.Errorf("writing backup: %w", err)
			}
		}
		if err := os.WriteFile(outPath, out, mode); err != nil {
			return err
		}
		fmt.Printf("patched %s (backup: %s.bak)\n", outPath, outPath)
	}
	return nil
}

func buildValue(target string, gen *generated, src []byte, cursorReplace bool) (key string, val any, err error) {
	switch target {
	case targetClaude:
		m, err := extractObject(src, "permissions")
		if err != nil {
			return "", nil, err
		}
		m["allow"] = orEmpty(gen.claudeAllow)
		m["ask"] = orEmpty(gen.claudeAsk)
		m["deny"] = orEmpty(gen.claudeDeny)
		return "permissions", m, nil
	case targetZed:
		return "tool_permissions", gen.zed, nil
	case targetCursor:
		m, err := extractObject(src, "permissions")
		if err != nil {
			return "", nil, err
		}
		allow, deny := gen.cursorAllow, gen.cursorDeny
		if !cursorReplace {
			allow = union(toStrings(m["allow"]), allow)
			deny = union(toStrings(m["deny"]), deny)
		}
		m["allow"] = orEmpty(allow)
		m["deny"] = orEmpty(deny)
		return "permissions", m, nil
	case targetCursorIDE:
		var m map[string]any
		if err := json.Unmarshal(src, &m); err != nil {
			return "", nil, fmt.Errorf("parsing existing permissions.json: %w", err)
		}
		if m == nil {
			m = map[string]any{}
		}
		m["terminalAllowlist"] = orEmpty(gen.cursorIDETerminal)
		m["mcpAllowlist"] = orEmpty(gen.cursorIDEMcp)
		return "", m, nil
	}
	return "", nil, fmt.Errorf("unknown target %q", target)
}

func outputFor(cfg *Config, target, home string) string {
	var p string
	switch target {
	case targetClaude:
		p = cfg.Outputs.Claude
		if p == "" {
			p = "~/.claude/settings.json"
		}
	case targetZed:
		p = cfg.Outputs.Zed
		if p == "" {
			p = "~/.config/zed/settings.json"
		}
	case targetCursor:
		p = cfg.Outputs.Cursor
		if p == "" {
			p = "~/.cursor/cli-config.json"
		}
	case targetCursorIDE:
		p = cfg.Outputs.CursorIDE
		if p == "" {
			p = "~/.cursor/permissions.json"
		}
	}
	return expandHome(p, home)
}

func resolveConfigPath(flagValue, home string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	candidates := []string{
		"permissions.yaml",
		filepath.Join(home, ".config", "agentperms", "permissions.yaml"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("no config found (tried %s); pass -config", strings.Join(candidates, ", "))
}

// orEmpty keeps empty rule lists marshaling as [] rather than null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// union keeps existing entries (and their order) and appends new ones.
func union(existing, generated []string) []string {
	seen := make(map[string]bool, len(existing))
	out := make([]string, 0, len(existing)+len(generated))
	for _, s := range existing {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range generated {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func marshalPretty(v any) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
