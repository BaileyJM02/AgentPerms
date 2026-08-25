package main

import (
	"fmt"
	"regexp"
	"strings"
)

type zedPattern struct {
	Pattern string `json:"pattern"`
}

type zedTool struct {
	Default       string       `json:"default,omitempty"`
	AlwaysAllow   []zedPattern `json:"always_allow,omitempty"`
	AlwaysConfirm []zedPattern `json:"always_confirm,omitempty"`
	AlwaysDeny    []zedPattern `json:"always_deny,omitempty"`
}

type zedPermissions struct {
	Default string              `json:"default,omitempty"`
	Tools   map[string]*zedTool `json:"tools"`
}

type generated struct {
	claudeAllow       []string
	claudeAsk         []string
	claudeDeny        []string
	cursorAllow       []string
	cursorDeny        []string
	cursorIDETerminal []string
	cursorIDEMcp      []string
	zed               *zedPermissions
	warnings          []string
}

func generate(cfg *Config, home string) (*generated, error) {
	g := &generated{zed: &zedPermissions{Default: cfg.ZedDefault, Tools: map[string]*zedTool{}}}
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if r.appliesTo(targetClaude) {
			g.addClaude(r.Action, claudeEntries(r, home))
		}
		if r.appliesTo(targetCursor) {
			g.addCursor(r.Action, cursorEntries(r, home))
		}
		if r.appliesToCursorIDE() {
			g.addCursorIDE(r)
		}
		if r.appliesTo(targetZed) {
			if err := g.addZed(r, home); err != nil {
				return nil, err
			}
		}
	}
	g.claudeAllow = dedupe(g.claudeAllow)
	g.claudeAsk = dedupe(g.claudeAsk)
	g.claudeDeny = dedupe(g.claudeDeny)
	g.cursorAllow = dedupe(g.cursorAllow)
	g.cursorDeny = dedupe(g.cursorDeny)
	g.cursorIDETerminal = dedupe(g.cursorIDETerminal)
	g.cursorIDEMcp = dedupe(g.cursorIDEMcp)
	return g, nil
}

// expandPrefixSpellings returns each accepted spelling of a command prefix:
// prefixes containing ~/ are emitted in both the literal-~ and absolute form.
func expandPrefixSpellings(prefix, home string) []string {
	if !strings.Contains(prefix, "~/") {
		return []string{prefix}
	}
	return []string{prefix, strings.ReplaceAll(prefix, "~/", home+"/")}
}

// tokenBody converts a command prefix into the token part of a Zed regex:
// spaces become \s+, a bare * token matches any single argument, and ~/
// tokens match either spelling of the home directory.
func tokenBody(prefix, home string) string {
	tokens := strings.Fields(prefix)
	parts := make([]string, len(tokens))
	for i, tok := range tokens {
		switch {
		case tok == "*":
			parts[i] = "[^ ]+"
		case strings.HasPrefix(tok, "~/"):
			parts[i] = "(~|" + regexp.QuoteMeta(home) + ")" + regexp.QuoteMeta(tok[1:])
		default:
			parts[i] = regexp.QuoteMeta(tok)
		}
	}
	return strings.Join(parts, `\s+`)
}

// commandPattern anchors a prefix as a Zed terminal regex. Prefixes ending in
// / accept any continuation; prefixes whose last token is a path accept a
// subpath or arguments; plain prefixes accept the bare command or arguments.
func commandPattern(prefix, home string) string {
	body := "^" + tokenBody(prefix, home)
	tokens := strings.Fields(prefix)
	last := tokens[len(tokens)-1]
	switch {
	case strings.HasSuffix(prefix, "/"):
		return body + ".*"
	case strings.ContainsAny(last, "/~"):
		return body + `([\s/].*)?$`
	default:
		return body + `(\s.*)?$`
	}
}

// flagsPattern matches prefix invocations that carry any of the given flags.
// Short flags require a following space or end-of-string so "-f" does not
// match "-files"; long flags match as prefixes so "--force" also catches
// "--force-with-lease".
func flagsPattern(prefix string, flags []string, home string) string {
	alts := make([]string, len(flags))
	for i, f := range flags {
		fe := regexp.QuoteMeta(f)
		if !strings.HasPrefix(f, "--") {
			fe += `(\s|$)`
		}
		alts[i] = fe
	}
	return "^" + tokenBody(prefix, home) + `\b.*(` + strings.Join(alts, "|") + ")"
}

// globToRegex converts a Claude/Cursor-style glob into an anchored regex for
// Zed's path tools. For ~/ globs, the home prefix matches either spelling and
// parent directories above the repo/wildcard segment are optional, so Zed's
// worktree-relative paths still match.
func globToRegex(glob, home string) string {
	rest := glob
	homeAlt := ""
	if strings.HasPrefix(glob, "~/") {
		homeAlt = "(~|" + regexp.QuoteMeta(home) + ")/"
		rest = glob[2:]
	}
	segs := strings.Split(rest, "/")
	optional := ""
	if homeAlt != "" {
		wild := len(segs)
		for i, s := range segs {
			if strings.ContainsAny(s, "*?") {
				wild = i
				break
			}
		}
		reqStart := wild
		if reqStart > len(segs)-2 {
			reqStart = len(segs) - 2
		}
		if reqStart > 0 {
			optional = "(" + homeAlt + segsToRegex(segs[:reqStart]) + "/)?"
			segs = segs[reqStart:]
			homeAlt = ""
		}
	}
	return "^" + homeAlt + optional + segsToRegex(segs) + "$"
}

func segsToRegex(segs []string) string {
	var b strings.Builder
	for i, seg := range segs {
		last := i == len(segs)-1
		if seg == "**" {
			if last {
				b.WriteString(".*")
			} else {
				b.WriteString("(.*/)?")
			}
			continue
		}
		for _, r := range seg {
			switch r {
			case '*':
				b.WriteString("[^/]*")
			case '?':
				b.WriteString("[^/]")
			default:
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
		if !last {
			b.WriteString("/")
		}
	}
	return b.String()
}

func expandHome(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return home + path[1:]
	}
	return path
}

func claudeEntries(r *Rule, home string) []string {
	switch r.Kind {
	case "command":
		var out []string
		for _, p := range expandPrefixSpellings(r.Prefix, home) {
			switch {
			case len(r.Flags) > 0:
				for _, f := range r.Flags {
					if strings.HasPrefix(f, "--") {
						out = append(out,
							fmt.Sprintf("Bash(%s %s*)", p, f),
							fmt.Sprintf("Bash(%s * %s*)", p, f))
					} else {
						out = append(out,
							fmt.Sprintf("Bash(%s %s *)", p, f),
							fmt.Sprintf("Bash(%s * %s *)", p, f))
					}
				}
			case strings.HasSuffix(p, "/"):
				out = append(out, fmt.Sprintf("Bash(%s*)", p))
			case strings.Contains(p, "*"):
				out = append(out, fmt.Sprintf("Bash(%s)", p), fmt.Sprintf("Bash(%s *)", p))
			default:
				out = append(out, fmt.Sprintf("Bash(%s)", p), fmt.Sprintf("Bash(%s:*)", p))
			}
		}
		if r.PipeTarget {
			out = append(out, fmt.Sprintf("Bash(* | %s *)", r.Prefix))
		}
		return out
	case "path":
		var out []string
		for _, a := range r.Access {
			switch a {
			case "read":
				out = append(out, "Read("+r.Glob+")", "Glob("+r.Glob+")", "Grep("+r.Glob+")")
			case "edit":
				out = append(out, "Edit("+r.Glob+")")
			case "write":
				out = append(out, "Write("+r.Glob+")")
			}
		}
		return out
	case "mcp":
		if r.Tool == "" || r.Tool == "*" {
			return []string{"mcp__" + r.Server}
		}
		return []string{"mcp__" + r.Server + "__" + r.Tool}
	case "fetch":
		return []string{"WebFetch(domain:" + r.Domain + ")"}
	}
	return nil
}

// cursorEntries emits Cursor rules. Constructs Cursor cannot express — ask
// rules (it prompts for anything undecided), flag conditions, and prefixes
// with wildcard/path tokens — are skipped; never silently broadened.
func cursorEntries(r *Rule, home string) []string {
	if r.Action == "ask" {
		return nil
	}
	switch r.Kind {
	case "command":
		if len(r.Flags) > 0 || strings.ContainsAny(r.Prefix, "*~/") {
			return nil
		}
		return []string{"Shell(" + r.Prefix + ")"}
	case "path":
		var out []string
		for _, a := range r.Access {
			switch a {
			case "read":
				out = append(out, "Read("+expandHome(r.Glob, home)+")")
			case "edit", "write":
				out = append(out, "Write("+expandHome(r.Glob, home)+")")
			}
		}
		return out
	case "mcp":
		if r.Tool == "" || r.Tool == "*" {
			return []string{"Mcp(" + r.Server + ")"}
		}
		return []string{"Mcp(" + r.Server + ":" + r.Tool + ")"}
	case "fetch":
		return []string{"WebFetch(" + r.Domain + ")"}
	}
	return nil
}

func (g *generated) addClaude(action string, entries []string) {
	switch action {
	case "allow":
		g.claudeAllow = append(g.claudeAllow, entries...)
	case "ask":
		g.claudeAsk = append(g.claudeAsk, entries...)
	case "deny":
		g.claudeDeny = append(g.claudeDeny, entries...)
	}
}

func (g *generated) addCursor(action string, entries []string) {
	switch action {
	case "allow":
		g.cursorAllow = append(g.cursorAllow, entries...)
	case "deny":
		g.cursorDeny = append(g.cursorDeny, entries...)
	}
}

// addCursorIDE emits entries for ~/.cursor/permissions.json, which the Cursor
// IDE agent reads. Only allow rules are expressible: terminalAllowlist takes
// plain command prefixes, mcpAllowlist takes server:tool. Path and fetch rules
// and ask/deny actions have no equivalent and are skipped (the IDE run mode
// decides anything the allowlist doesn't).
func (g *generated) addCursorIDE(r *Rule) {
	if r.Action != "allow" {
		return
	}
	switch r.Kind {
	case "command":
		if len(r.Flags) > 0 || strings.ContainsAny(r.Prefix, "*~/") {
			return
		}
		g.cursorIDETerminal = append(g.cursorIDETerminal, r.Prefix)
	case "mcp":
		tool := r.Tool
		if tool == "" {
			tool = "*"
		}
		g.cursorIDEMcp = append(g.cursorIDEMcp, r.Server+":"+tool)
	}
}

func (g *generated) addZed(r *Rule, home string) error {
	switch r.Kind {
	case "command":
		p := commandPattern(r.Prefix, home)
		if len(r.Flags) > 0 {
			p = flagsPattern(r.Prefix, r.Flags, home)
		}
		return g.addZedPattern(r, "terminal", p)
	case "path":
		for _, a := range r.Access {
			var tool string
			switch a {
			case "edit":
				tool = "edit_file"
			case "write":
				tool = "write_file"
			case "read":
				continue // Zed does not permission-gate read tools
			}
			if err := g.addZedPattern(r, tool, globToRegex(r.Glob, home)); err != nil {
				return err
			}
		}
		return nil
	case "mcp":
		if r.Tool == "" || r.Tool == "*" {
			g.warnings = append(g.warnings, fmt.Sprintf("zed needs an explicit mcp tool name; %s not emitted for zed", r.describe()))
			return nil
		}
		name := "mcp:" + r.Server + ":" + r.Tool
		t := g.zedTool(name)
		t.Default = map[string]string{"allow": "allow", "ask": "confirm", "deny": "deny"}[r.Action]
		return nil
	case "fetch":
		return g.addZedPattern(r, "fetch", regexp.QuoteMeta(r.Domain))
	}
	return nil
}

func (g *generated) addZedPattern(r *Rule, tool, pattern string) error {
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("%s: invalid zed regex %q: %v", r.describe(), pattern, err)
	}
	t := g.zedTool(tool)
	var list *[]zedPattern
	switch r.Action {
	case "allow":
		list = &t.AlwaysAllow
	case "ask":
		list = &t.AlwaysConfirm
	case "deny":
		list = &t.AlwaysDeny
	}
	for _, p := range *list {
		if p.Pattern == pattern {
			return nil
		}
	}
	*list = append(*list, zedPattern{Pattern: pattern})
	return nil
}

func (g *generated) zedTool(name string) *zedTool {
	t := g.zed.Tools[name]
	if t == nil {
		t = &zedTool{}
		g.zed.Tools[name] = t
	}
	return t
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
