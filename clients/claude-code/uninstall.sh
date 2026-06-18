#!/usr/bin/env bash
# Removes the agistry Claude Code client from ~/.claude and unwires its hooks from
# ~/.claude/settings.json. Leaves ~/.config/agistry/client.env in place.
set -euo pipefail

CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
SETTINGS="$CLAUDE_DIR/settings.json"
REG="$CLAUDE_DIR/hooks/agistry-register.sh"
DEREG="$CLAUDE_DIR/hooks/agistry-deregister.sh"

echo "Removing agistry client from $CLAUDE_DIR"

# unwire hooks first (needs the command paths)
if command -v jq >/dev/null 2>&1 && [ -f "$SETTINGS" ]; then
  cp "$SETTINGS" "$SETTINGS.bak.$(date +%s)"
  tmp="$(mktemp)"
  jq --arg reg "$REG" --arg dereg "$DEREG" '
    if .hooks then
      .hooks.SessionStart = ((.hooks.SessionStart // []) | map(select((.hooks // []) | map(.command) | index($reg) | not)))
      | .hooks.SessionEnd = ((.hooks.SessionEnd // []) | map(select((.hooks // []) | map(.command) | index($dereg) | not)))
    else . end
  ' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
  echo "  hooks unwired from $SETTINGS (backup saved)"
else
  echo "  skipped settings.json (no jq or no file) — remove the agistry hooks by hand."
fi

rm -f "$CLAUDE_DIR/hooks/agistry-register.sh" "$CLAUDE_DIR/hooks/agistry-deregister.sh" "$CLAUDE_DIR/hooks/agistry-heartbeat.sh"
rm -rf "$CLAUDE_DIR/skills/agistry" "$CLAUDE_DIR/agistry-channel"
echo "  removed hooks, skill, and channel"
echo "  left ~/.config/agistry/client.env in place (delete it yourself if you want)"
echo "Done."
