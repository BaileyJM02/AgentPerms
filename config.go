package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	targetClaude    = "claude"
	targetZed       = "zed"
	targetCursor    = "cursor"
	targetCursorIDE = "cursor-ide"
)

var allTargets = []string{targetClaude, targetZed, targetCursor, targetCursorIDE}

type Config struct {
	Outputs struct {
		Claude    string `yaml:"claude"`
		Zed       string `yaml:"zed"`
		Cursor    string `yaml:"cursor"`
		CursorIDE string `yaml:"cursor_ide"`
	} `yaml:"outputs"`
	// ZedDefault becomes agent.tool_permissions.default (allow|confirm|deny).
	ZedDefault string `yaml:"zed_default"`
	Rules      []Rule `yaml:"rules"`
}

type Rule struct {
	Kind   string `yaml:"kind"`   // command | path | mcp | fetch
	Action string `yaml:"action"` // allow | ask | deny

	// command: space-separated prefix. Tokens may be "*" (any single token)
	// or contain ~/ (matched in both ~ and absolute spellings). A trailing /
	// means "any continuation", e.g. "gh api repos/".
	Prefix     string   `yaml:"prefix"`
	Flags      []string `yaml:"flags"`       // command: only when one of these flags is present
	PipeTarget bool     `yaml:"pipe_target"` // command: also allow as `anything | cmd` (claude)

	Glob   string   `yaml:"glob"`   // path: glob, may start with ~/
	Access []string `yaml:"access"` // path: read | edit | write

	Server string `yaml:"server"` // mcp
	Tool   string `yaml:"tool"`   // mcp

	Domain string `yaml:"domain"` // fetch

	Only []string `yaml:"only"` // restrict rule to these targets
	Note string   `yaml:"note"`
}

func (r *Rule) appliesTo(target string) bool {
	return len(r.Only) == 0 || slices.Contains(r.Only, target)
}

// appliesToCursorIDE treats "cursor" in an only list as covering both Cursor
// targets, so existing cursor-only rules reach the IDE without duplication.
func (r *Rule) appliesToCursorIDE() bool {
	return r.appliesTo(targetCursorIDE) || slices.Contains(r.Only, targetCursor)
}

func (r *Rule) describe() string {
	id := r.Prefix
	for _, alt := range []string{r.Glob, r.Server, r.Domain, r.Note} {
		if id != "" {
			break
		}
		id = alt
	}
	return fmt.Sprintf("%s/%s(%s)", r.Kind, r.Action, id)
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.ZedDefault != "" && !slices.Contains([]string{"allow", "confirm", "deny"}, c.ZedDefault) {
		return fmt.Errorf("zed_default must be allow|confirm|deny, got %q", c.ZedDefault)
	}
	for i := range c.Rules {
		r := &c.Rules[i]
		where := fmt.Sprintf("rules[%d] %s", i, r.describe())
		if !slices.Contains([]string{"command", "path", "mcp", "fetch"}, r.Kind) {
			return fmt.Errorf("%s: kind must be command|path|mcp|fetch", where)
		}
		if !slices.Contains([]string{"allow", "ask", "deny"}, r.Action) {
			return fmt.Errorf("%s: action must be allow|ask|deny", where)
		}
		for _, t := range r.Only {
			if !slices.Contains(allTargets, t) {
				return fmt.Errorf("%s: unknown target %q in only", where, t)
			}
		}
		switch r.Kind {
		case "command":
			if strings.TrimSpace(r.Prefix) == "" {
				return fmt.Errorf("%s: command rules need prefix", where)
			}
			if r.PipeTarget && (len(r.Flags) > 0 || strings.ContainsAny(r.Prefix, "*~/")) {
				return fmt.Errorf("%s: pipe_target only combines with a plain command prefix", where)
			}
		case "path":
			if r.Glob == "" {
				return fmt.Errorf("%s: path rules need glob", where)
			}
			if len(r.Access) == 0 {
				return fmt.Errorf("%s: path rules need access (read|edit|write)", where)
			}
			for _, a := range r.Access {
				if !slices.Contains([]string{"read", "edit", "write"}, a) {
					return fmt.Errorf("%s: access entries must be read|edit|write, got %q", where, a)
				}
			}
		case "mcp":
			if r.Server == "" {
				return fmt.Errorf("%s: mcp rules need server", where)
			}
		case "fetch":
			if r.Domain == "" {
				return fmt.Errorf("%s: fetch rules need domain", where)
			}
		}
		if r.Kind != "command" && (len(r.Flags) > 0 || r.PipeTarget) {
			return fmt.Errorf("%s: flags/pipe_target only apply to command rules", where)
		}
	}
	return nil
}
