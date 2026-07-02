#!/usr/bin/env bash
# Removes the agistry Claude Code client from ~/.claude and unwires its hooks from
# ~/.claude/settings.json. Leaves ~/.config/agistry/client.env in place.
set -euo pipefail

CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
SETTINGS="$CLAUDE_DIR/settings.json"
REG="$CLAUDE_DIR/hooks/agistry-register.sh"
DEREG="$CLAUDE_DIR/hooks/agistry-deregister.sh"
SL="$CLAUDE_DIR/statusline/agistry-statusline.sh"

echo "Removing agistry client from $CLAUDE_DIR"

# unwire hooks + statusline first (needs the command paths). The statusLine is only
# dropped when it still points at our script, so a user's own one is left untouched.
if command -v jq >/dev/null 2>&1 && [ -f "$SETTINGS" ]; then
  cp "$SETTINGS" "$SETTINGS.bak.$(date +%s)"
  tmp="$(mktemp)"
  jq --arg reg "$REG" --arg dereg "$DEREG" --arg sl "$SL" '
    (if .hooks then
      .hooks.SessionStart = ((.hooks.SessionStart // []) | map(select(([.hooks[]?.command] | map(test("agistry-register")) | any) | not)))
      | .hooks.SessionEnd = ((.hooks.SessionEnd // []) | map(select(([.hooks[]?.command] | map(test("agistry-deregister")) | any) | not)))
    else . end)
    | (if (.statusLine.command // "") == $sl then del(.statusLine) else . end)
  ' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
  echo "  hooks + statusline unwired from $SETTINGS (backup saved)"
else
  echo "  skipped settings.json (no jq or no file) — remove the agistry hooks by hand."
fi

rm -f "$CLAUDE_DIR/hooks/agistry-register.sh" "$CLAUDE_DIR/hooks/agistry-deregister.sh" "$CLAUDE_DIR/hooks/agistry-heartbeat.sh"
rm -rf "$CLAUDE_DIR/skills/agistry" "$CLAUDE_DIR/agistry-channel" "$CLAUDE_DIR/statusline"
echo "  removed hooks, skill, channel, and statusline"
echo "  left ~/.config/agistry/client.env in place (delete it yourself if you want)"
echo "Done."
