package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

const testHome = "/Users/bailey"

func TestCommandPattern(t *testing.T) {
	re := regexp.MustCompile(commandPattern("git diff", testHome))
	for _, ok := range []string{"git diff", "git diff --stat HEAD~1"} {
		if !re.MatchString(ok) {
			t.Errorf("expected match: %q", ok)
		}
	}
	for _, bad := range []string{"git difftool", "xgit diff", "git push"} {
		if re.MatchString(bad) {
			t.Errorf("unexpected match: %q", bad)
		}
	}
}

func TestCommandPatternMidWildcard(t *testing.T) {
	re := regexp.MustCompile(commandPattern("gh -R * pr view", testHome))
	for _, ok := range []string{"gh -R nala/rafiki-backend pr view", "gh -R x pr view 42 --web"} {
		if !re.MatchString(ok) {
			t.Errorf("expected match: %q", ok)
		}
	}
	for _, bad := range []string{"gh pr view", "gh -R x pr list", "gh -R x y pr view"} {
		if re.MatchString(bad) {
			t.Errorf("unexpected match: %q", bad)
		}
	}
}

func TestCommandPatternHomePath(t *testing.T) {
	re := regexp.MustCompile(commandPattern("cd ~/Development", testHome))
	for _, ok := range []string{
		"cd ~/Development",
		"cd ~/Development/nala-remit",
		"cd /Users/bailey/Development/go-library",
	} {
		if !re.MatchString(ok) {
			t.Errorf("expected match: %q", ok)
		}
	}
	for _, bad := range []string{"cd /tmp", "cd /Users/bailey/Developmentx", "cd /Users/other/Development"} {
		if re.MatchString(bad) {
			t.Errorf("unexpected match: %q", bad)
		}
	}
}

func TestCommandPatternTrailingSlash(t *testing.T) {
	re := regexp.MustCompile(commandPattern("gh api repos/", testHome))
	if !re.MatchString("gh api repos/nala/rafiki-backend/pulls") {
		t.Error("expected trailing-slash prefix to match continuations")
	}
	if re.MatchString("gh api user") {
		t.Error("unexpected match for different endpoint")
	}
}

func TestFlagsPattern(t *testing.T) {
	re := regexp.MustCompile(flagsPattern("git push", []string{"--force", "-f"}, testHome))
	for _, ok := range []string{
		"git push --force",
		"git push origin main --force-with-lease",
		"git push -f",
		"git push origin -f main",
	} {
		if !re.MatchString(ok) {
			t.Errorf("expected match: %q", ok)
		}
	}
	for _, bad := range []string{"git push origin main", "git push -files", "git pull --force"} {
		if re.MatchString(bad) {
			t.Errorf("unexpected match: %q", bad)
		}
	}

	re = regexp.MustCompile(flagsPattern("git -C * push", []string{"--force", "-f"}, testHome))
	if !re.MatchString("git -C ~/Development/nala-remit push --force") {
		t.Error("expected -C force push to match")
	}
	if re.MatchString("git -C x push origin main") {
		t.Error("unexpected match without force flag")
	}
}

func TestGlobToRegexOptionalParents(t *testing.T) {
	re := regexp.MustCompile("(?i)" + globToRegex("~/Development/nala-*/**", testHome))
	for _, ok := range []string{
		"~/Development/nala-remit/main.go",
		"/Users/bailey/Development/nala-api/a/b/c.go",
		"nala-remit/services/foo.go", // worktree-relative
		"Nala-Api/main.go",           // zed matching is case-insensitive
	} {
		if !re.MatchString(ok) {
			t.Errorf("expected match: %q", ok)
		}
	}
	for _, bad := range []string{
		"rafiki-backend/main.go",
		"/Users/other/Development/nala-remit/main.go",
		"services/nala-helper/foo.go",
	} {
		if re.MatchString(bad) {
			t.Errorf("unexpected match: %q", bad)
		}
	}

	re = regexp.MustCompile(globToRegex("~/Development/go-library/**", testHome))
	if !re.MatchString("go-library/pkg/x.go") || !re.MatchString("/Users/bailey/Development/go-library/x.go") {
		t.Error("expected fixed-dir glob to match relative and absolute forms")
	}
	if re.MatchString("other-lib/pkg/x.go") {
		t.Error("fixed dir must stay required")
	}
}

func TestGlobToRegexSuffix(t *testing.T) {
	re := regexp.MustCompile(globToRegex("**/*.env", testHome))
	for _, ok := range []string{".env", "prod.env", "config/deep/.env"} {
		if !re.MatchString(ok) {
			t.Errorf("expected match: %q", ok)
		}
	}
	for _, bad := range []string{".environment", "env", "a/env.example"} {
		if re.MatchString(bad) {
			t.Errorf("unexpected match: %q", bad)
		}
	}
}

func TestCursorIDEGeneration(t *testing.T) {
	cfg := &Config{Rules: []Rule{
		{Kind: "command", Action: "allow", Prefix: "git status"},
		{Kind: "command", Action: "allow", Prefix: "git push", Only: []string{"claude"}},
		{Kind: "command", Action: "allow", Prefix: "cd", Only: []string{"cursor"}},
		{Kind: "command", Action: "ask", Prefix: "gh pr create"},
		{Kind: "command", Action: "allow", Prefix: "gh -R * pr view"},
		{Kind: "command", Action: "allow", Prefix: "cd ~/Development"},
		{Kind: "mcp", Action: "allow", Server: "atlassian", Tool: "getJiraIssue"},
		{Kind: "mcp", Action: "allow", Server: "datadog"},
		{Kind: "path", Action: "allow", Glob: "~/Development/**", Access: []string{"read"}},
		{Kind: "fetch", Action: "allow", Domain: "github.com"},
	}}
	g, err := generate(cfg, testHome)
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal := []string{"git status", "cd"}
	wantMcp := []string{"atlassian:getJiraIssue", "datadog:*"}
	b1, _ := json.Marshal(g.cursorIDETerminal)
	b2, _ := json.Marshal(wantTerminal)
	if string(b1) != string(b2) {
		t.Errorf("terminalAllowlist = %s, want %s", b1, b2)
	}
	b1, _ = json.Marshal(g.cursorIDEMcp)
	b2, _ = json.Marshal(wantMcp)
	if string(b1) != string(b2) {
		t.Errorf("mcpAllowlist = %s, want %s", b1, b2)
	}
}

const sampleJSONC = `{
  // leading comment with a "permissions" decoy in it
  "before": "a string with \"permissions\": {not real}",
  "agent": {
    "tool_permissions": {
      "tools": {"terminal": {"always_confirm": [{"pattern": ".* -f(.*)"}]}}
    }, // trailing comment
    "other": true
  }
}`

func TestSpliceKeyPreservesEverythingElse(t *testing.T) {
	val := map[string]any{"default": "confirm"}
	out, err := spliceKey([]byte(sampleJSONC), "tool_permissions", val)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"leading comment", "// trailing comment", `"other": true`, `"default": "confirm"`} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "always_confirm") {
		t.Errorf("old value not replaced:\n%s", s)
	}
}

func TestFindKeyValueIgnoresStringsAndComments(t *testing.T) {
	if _, _, err := findKeyValue([]byte(sampleJSONC), "tool_permissions"); err != nil {
		t.Fatalf("unique key should be found: %v", err)
	}
	dup := sampleJSONC + `
// second copy below
` + sampleJSONC
	if _, _, err := findKeyValue([]byte(dup), "tool_permissions"); err == nil {
		t.Fatal("duplicate keys must be an error")
	}
	if _, _, err := findKeyValue([]byte(sampleJSONC), "missing"); err == nil {
		t.Fatal("missing key must be an error")
	}
}

func TestExtractObjectAndUnion(t *testing.T) {
	m, err := extractObject([]byte(`{"permissions": {"allow": ["Shell(ls)"], "deny": []}}`), "permissions")
	if err != nil {
		t.Fatal(err)
	}
	got := union(toStrings(m["allow"]), []string{"Shell(ls)", "Shell(go)"})
	want := []string{"Shell(ls)", "Shell(go)"}
	b1, _ := json.Marshal(got)
	b2, _ := json.Marshal(want)
	if string(b1) != string(b2) {
		t.Errorf("union = %s, want %s", b1, b2)
	}
}
