# agistry × tmux

A tmux session switcher that tells you what each session is *about* and whether
its agent needs you — for hosts running many Claude Code, Codex, opencode and pi
sessions whose tmux names (`proj-myrepo-claude-2`) say nothing.

```
↑↓ switch session
● needs you  ◐ working  ✓ done   ↻ 2s
✓ PR-786 · reviewer  proj-myrepo-claude 4m
◐ flaky-e2e-fix · dev-lane  proj-myrepo-claude-2
● Release notes draft  proj-myrepo-claude-3 2m
  Review benchmark status  proj-myrepo-codex
```

| Piece | What it does |
| --- | --- |
| `tmux-main` | Attaches the "main" tmux client and records its pid/tty in `~/.cache`, so the sidebar knows which client to switch. Remembers the main session by id, since names change. |
| `tmux-sidebar` | fzf switcher for the `tmux-main` client. Per session: state glyph, **label**, the tmux name, and how long it has been waiting/done. Refreshes every 2s from one `ps` + one `tmux` call. |
| `agent-state` | Writes `@agent_kind`, `@agent_state` (`working`/`waiting`/`done`/`idle`) and `@agent_since` on the agent's own pane (`$TMUX_PANE`). Called by every agent's hooks; with no state argument it maps a Claude/Codex hook event read from stdin. |
| `adapters/` | opencode plugin and pi extension that call `agent-state`. Claude Code and Codex need no adapter — their hooks call `agent-state` directly. |
| `install.sh` | Symlinks the scripts into `~/bin` and wires every agent found on the host. Idempotent; `--uninstall` removes the hooks and agent-state. |

## Labels: never rename, annotate

Session names stay as they are. A shell hook that auto-names sessions (a zsh
`precmd` calling `rename-session`) would rename them back on the next prompt, and
agents that start sessions address them by name (`send-keys -t`), so a rename would
break them. The sidebar shows a label instead:

1. **Claude Code** — the agistry `task · role` the session joined with. Read from the
   local state files (`state/by-pid/<claude pid>` → `state/<sid>.json`), no HTTP;
   a by-pid file is trusted only if its recorded start time matches the live process.
2. **Fallback, every agent** — the terminal title the agent sets itself, cleaned:

   | Agent | Title it sets | Label |
   | --- | --- | --- |
   | Claude Code | `✳ <summary>` (`⑂` suffix on forks) | summary |
   | Codex | `<summary> \| <project>` | summary |
   | opencode | `OC \| <session title>` / `OpenCode` | title |
   | pi | `π - <name> - <dir>` / `π - <dir>` | name (only after `/name`) |

## States

| State | Claude Code / Codex hook | opencode event | pi event |
| --- | --- | --- | --- |
| `working` | `UserPromptSubmit`, `PreToolUse`, `PostToolUse` | `session.status` busy | `agent_start` |
| `waiting` | `PermissionRequest`, `Notification` (`permission_prompt`, `elicitation_dialog`), `PreToolUse` of `AskUserQuestion` | `permission.asked`, `question.asked` | `ui_prompt_start` |
| `done` | `Stop`, `StopFailure`, `Interrupt`; `idle_prompt` only ends a stuck `working` (Esc fires no `Stop`) | all sessions idle, no prompt pending | `agent_settled` |
| cleared | `SessionEnd` | — | `session_shutdown` |

A state is shown only while the pane's foreground command is still that agent, so
one left behind by a crash disappears once the pane is back at a shell.

## Install

```bash
clients/tmux/install.sh
```

That one command covers everything on this host:

- links `tmux-main`, `tmux-sidebar` and `agent-state` into `~/bin` (an existing real
  file there is kept as `<name>.bak.<timestamp>`);
- wires hooks for each agent it finds (`~/.claude`, `~/.codex`, `~/.config/opencode`,
  `~/.pi/agent`); agents that aren't installed are skipped — re-run after installing one.

What it can't do for you:

- **Codex** skips new hooks until trusted: run `/hooks` once in a Codex session.
- **Restart** running Codex, opencode and pi sessions to load the hooks. Claude Code
  picks them up live.
- **Prerequisites:** tmux 3.0+, fzf, jq. The installer warns if fzf or tmux is missing.
- **agistry labels** need the [Claude Code client](../claude-code/) installed (it
  writes the state files the sidebar reads). Without it, Claude sessions fall back
  to their pane title.

## Use

Outside tmux, split your terminal into two panes: run `tmux-main` in the wide one
and `tmux-sidebar` in a narrow one beside it. Moving the cursor in the sidebar
switches the main client to that session.

Debug with `tmux-sidebar --list`, or `tmux list-panes -a -F '#{session_name} #{@agent_kind}/#{@agent_state}'`.
