#!/usr/bin/env bash
# Installs the tmux client: agent-state + tmux-sidebar into ~/bin (symlinks, so
# edits here go live), and wires agent-state into every agent found on this host:
#   Claude Code  hooks in ~/.claude/settings.json      (merged, backup saved)
#   Codex        hooks in ~/.codex/hooks.json          (merged, backup saved; trust once via /hooks)
#   opencode     plugin  ~/.config/opencode/plugins/agent-state.js   (symlink)
#   pi           extension ~/.pi/agent/extensions/agent-state.ts     (symlink)
# Safe to re-run. --uninstall removes all of it (the sidebar link stays, see below).
#
# Usage: ./install.sh [--uninstall]
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${BIN_DIR:-$HOME/bin}"
STATE="$BIN/agent-state"
UNINSTALL=0
case "${1:-}" in
  --uninstall) UNINSTALL=1 ;;
  "") ;;
  -h|--help) sed -n '2,11p' "$0"; exit 0 ;;
  *) echo "unknown arg: $1" >&2; exit 1 ;;
esac

command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }

# Link $2 -> $1, keeping a real file that is in the way as a timestamped backup.
link() {
  mkdir -p "$(dirname "$2")"
  if [ -e "$2" ] && [ ! -L "$2" ]; then
    mv "$2" "$2.bak.$(date +%s)"; echo "  backed up existing $2"
  fi
  ln -sfn "$1" "$2"; echo "  $2 -> $1"
}
unlink_ours() { [ -L "$1" ] && [ "$(readlink "$1")" = "$2" ] && rm -f "$1" && echo "  removed $1"; true; }

# Rewrite a Claude-style hooks file: drop every group that runs agent-state, then
# (unless uninstalling) add one group per event. $3 is a JSON array of
# [event, matcher-or-null] pairs.
wire_hooks() { # file kind events-json
  local file="$1" kind="$2" events="$3" tmp
  [ -f "$file" ] || echo '{}' > "$file"
  cp "$file" "$file.bak.$(date +%s)"
  tmp="$(mktemp)"
  jq --arg cmd "$STATE $kind" --argjson ev "$events" --argjson add "$((1 - UNINSTALL))" '
    def ours: [.hooks[]?.command // ""] | map(test("agent-state")) | any;
    .hooks = ((.hooks // {}) | with_entries(.value |= map(select(ours | not)))
                             | with_entries(select(.value | length > 0)))
    | if $add == 1 then
        reduce $ev[] as [$e, $m] (.;
          .hooks[$e] = ((.hooks[$e] // []) + [
            ({hooks: [{type: "command", command: $cmd}]}
             + (if $m == null then {} else {matcher: $m} end))]))
      else . end
    | if .hooks == {} then del(.hooks) else . end
  ' "$file" > "$tmp" && mv "$tmp" "$file"
  echo "  hooks $([ $UNINSTALL = 1 ] && echo removed from || echo wired into) $file (backup saved)"
}

TOOL_EVENTS='"*"'
CLAUDE_EVENTS="[[\"SessionStart\",null],[\"UserPromptSubmit\",null],[\"PreToolUse\",$TOOL_EVENTS],
  [\"PostToolUse\",$TOOL_EVENTS],[\"PermissionRequest\",$TOOL_EVENTS],[\"Notification\",null],
  [\"Stop\",null],[\"StopFailure\",null],[\"SessionEnd\",null]]"
CODEX_EVENTS="[[\"SessionStart\",null],[\"UserPromptSubmit\",null],[\"PreToolUse\",$TOOL_EVENTS],
  [\"PostToolUse\",$TOOL_EVENTS],[\"PermissionRequest\",$TOOL_EVENTS],[\"Stop\",null],
  [\"Interrupt\",null],[\"SessionEnd\",null]]"

echo "$([ $UNINSTALL = 1 ] && echo Uninstalling || echo Installing) agistry tmux client"

if [ $UNINSTALL = 0 ]; then
  link "$HERE/agent-state" "$STATE"
  link "$HERE/tmux-sidebar" "$BIN/tmux-sidebar"
fi

[ -d "$HOME/.claude" ] && wire_hooks "$HOME/.claude/settings.json" claude "$CLAUDE_EVENTS"
if [ -d "$HOME/.codex" ]; then
  wire_hooks "$HOME/.codex/hooks.json" codex "$CODEX_EVENTS"
  [ $UNINSTALL = 0 ] && echo "  codex: new hooks are skipped until trusted — run /hooks in a codex session once"
fi

OC="$HOME/.config/opencode/plugins/agent-state.js"
PI="$HOME/.pi/agent/extensions/agent-state.ts"
if [ $UNINSTALL = 1 ]; then
  unlink_ours "$OC" "$HERE/adapters/opencode-agent-state.js"
  unlink_ours "$PI" "$HERE/adapters/pi-agent-state.ts"
  unlink_ours "$STATE" "$HERE/agent-state"
  echo "  left $BIN/tmux-sidebar in place (it works without agent-state); restore a .bak if you want the old one"
else
  [ -d "$HOME/.config/opencode" ] && link "$HERE/adapters/opencode-agent-state.js" "$OC"
  [ -d "$HOME/.pi/agent" ] && link "$HERE/adapters/pi-agent-state.ts" "$PI"
fi

echo "Done. Claude picks up hooks live; restart codex, opencode and pi sessions to load theirs."
