# agentperms

One tool-neutral `permissions.yaml` → native permission config for three agents.
The YAML contains no per-tool syntax; the translator produces each format.

| Target | File | Section owned |
|---|---|---|
| `claude` | `~/.claude/settings.json` | `permissions.allow/ask/deny` (other keys, incl. `additionalDirectories`, preserved) |
| `zed` | `~/.config/zed/settings.json` | `agent.tool_permissions` (JSONC-safe splice — comments elsewhere survive) |
| `cursor` | `~/.cursor/cli-config.json` | `permissions.allow/deny` (union-merged by default) |
| `cursor-ide` | `~/.cursor/permissions.json` | `terminalAllowlist` + `mcpAllowlist` (whole-file merge; other keys like `autoRun` preserved; created if missing) |

```sh
go build -o agentperms .
./agentperms                 # preview generated sections
./agentperms -write          # splice into the live files (writes <file>.bak first)
./agentperms -targets zed    # limit to one target
./agentperms -cursor-replace # replace cursor lists instead of merging
```

Config resolution: `-config <path>`, else `./permissions.yaml`, else
`~/.config/agentperms/permissions.yaml`.

## Rule model

```yaml
zed_default: confirm          # agent.tool_permissions.default

rules:
  - kind: command             # command | path | mcp | fetch
    action: allow             # allow | ask | deny
    prefix: gh -R * pr view   # "*" = any one token; "~/..." = both home spellings;
                              # trailing "/" = any continuation
    flags: ["--force", "-f"]  # only when one of these flags is present
    pipe_target: true         # also allow as `anything | cmd` (claude)
  - kind: path
    action: deny
    access: [edit, write]     # read | edit | write
    glob: "**/*.pem"
  - {kind: mcp, action: allow, server: atlassian, tool: getJiraIssue}
  - {kind: fetch, action: allow, domain: docs.rs}
  # optional on any rule: only: [claude, zed]   note: free text
```

## Translation semantics

| Rule | Claude | Zed | Cursor |
|---|---|---|---|
| `command prefix` | `Bash(p)` + `Bash(p:*)` | `^p(\s.*)?$` | `Shell(p)` |
| prefix with `*` token | `Bash(p)` + `Bash(p *)` | `*` → `[^ ]+` | — skipped |
| prefix with `~/` | both spellings | `(~\|/abs/home)` alternation | — skipped |
| trailing `/` | `Bash(p*)` | `^p.*` | — skipped |
| `+ flags` | `Bash(p f*)` / `Bash(p * f*)` per flag | `^p\b.*(f1\|f2)` | — skipped |
| `pipe_target` | also `Bash(* \| p *)` | plain command | plain command |
| `path` | `Read/Glob/Grep/Edit/Write(glob)` | `edit_file`/`write_file` regex; reads not gated | `Read/Write(glob)`, `~` expanded |
| `mcp` | `mcp__server__tool` | `mcp:server:tool` default | `Mcp(server:tool)` |
| `fetch` | `WebFetch(domain:d)` | `fetch` pattern | `WebFetch(d)` |
| `action: ask` | `ask` list | `always_confirm` | — skipped (prompts by default) |

Zed path regexes make the home-anchored parent directories optional (the repo
directory itself stays required), so worktree-relative paths still match.
"— skipped" is by design, never a silent broadening: Cursor prompts for
whatever its config doesn't decide.

## Scope & caveats

- Zed's `agent.tool_permissions` governs **only the native Zed agent**. Claude
  Code inside Zed follows `~/.claude/settings.json`; Cursor inside Zed follows
  `~/.cursor/cli-config.json`. That's why one YAML feeding all three is useful.
- Cursor's file is **machine-mutated** ("Allow always" clicks append to it), so
  the default is union-merge; use `-cursor-replace` to make YAML authoritative.
- The `cursor-ide` target feeds the **IDE agent panel**, which does not read
  cli-config.json. Defining `terminalAllowlist`/`mcpAllowlist` in
  permissions.json fully replaces the IDE's in-app allowlists for those types —
  intentional: the YAML is authoritative. Only allow rules with plain prefixes
  are expressible; `only: [cursor]` rules also apply to `cursor-ide`.
- Claude's `.env`-style protections live in a PreToolUse hook (so `.env.example`
  stays editable); the YAML mirrors them as deny rules for zed+cursor only.
- After `-write`, re-sync chezmoi: `chezmoi re-add ~/.claude/settings.json
  ~/.config/zed/settings.json` (cli-config.json is state — manage loosely).
