#!/usr/bin/env bash
# Removes the agistry Claude Code client from ~/.claude and unwires its hooks from
# ~/.claude/settings.json. Leaves ~/.config/agistry/client.env in place.
set -euo pipefail

CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
SETTINGS="$CLAUDE_DIR/settings.json"
REG="$CLAUDE_DIR/hooks/agistry-register.sh"
DEREG="$CLAUDE_DIR/hooks/agistry-deregister.sh"
SL="$CLAUDE_DIR/statusline/agistry-statusline.sh"
SL_PREV="$CLAUDE_DIR/statusline/agistry-statusline.prev.json"

echo "Removing agistry client from $CLAUDE_DIR"

# unwire hooks + statusline first (needs the command paths). The statusLine is only
# touched when it still points at our script: the one it wrapped (if any) is put
# back, otherwise it is dropped. A user's own one is left untouched.
if command -v jq >/dev/null 2>&1 && [ -f "$SETTINGS" ]; then
  cp "$SETTINGS" "$SETTINGS.bak.$(date +%s)"
  tmp="$(mktemp)"
  PREV_SL='null'; [ -f "$SL_PREV" ] && PREV_SL="$(jq -c 'del(.agistry_replaced)' "$SL_PREV" 2>/dev/null || echo null)"
  jq --arg reg "$REG" --arg dereg "$DEREG" --arg sl "$SL" --argjson prev "$PREV_SL" '
    (if .hooks then
      .hooks.SessionStart = ((.hooks.SessionStart // []) | map(select(([.hooks[]?.command] | map(test("agistry-register")) | any) | not)))
      | .hooks.SessionEnd = ((.hooks.SessionEnd // []) | map(select(([.hooks[]?.command] | map(test("agistry-deregister")) | any) | not)))
    else . end)
    | (if (.statusLine.command // "") != $sl then .
       elif $prev != null then .statusLine = $prev
       else del(.statusLine) end)
  ' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
  echo "  hooks + statusline unwired from $SETTINGS (backup saved)"
else
  echo "  skipped settings.json (no jq or no file) — remove the agistry hooks by hand."
fi

# unregister the channel MCP server, but only if it is this profile's copy
if command -v claude >/dev/null 2>&1; then
  if [ "$CLAUDE_DIR" = "$HOME/.claude" ]; then cli() { env -u CLAUDE_CONFIG_DIR claude "$@"; }
  else cli() { CLAUDE_CONFIG_DIR="$CLAUDE_DIR" claude "$@"; }; fi
  if cli mcp get agistry-channel 2>/dev/null | grep -qF "$CLAUDE_DIR/agistry-channel/"; then
    cli mcp remove -s user agistry-channel >/dev/null 2>&1 && echo "  channel MCP server unregistered"
  fi
fi

rm -f "$CLAUDE_DIR/hooks/agistry-register.sh" "$CLAUDE_DIR/hooks/agistry-deregister.sh" "$CLAUDE_DIR/hooks/agistry-heartbeat.sh"
# Providers are host-wide (~/.config/agistry/providers) and may serve other profiles or
# Codex, so they are left running; clients/providers/install.sh --uninstall removes them.
# $CLAUDE_DIR/providers is only an old per-profile copy.
rm -rf "$CLAUDE_DIR/skills/agistry" "$CLAUDE_DIR/agistry-channel" "$CLAUDE_DIR/providers"
# only our files: the user may keep their own scripts in statusline/
rm -f "$SL" "$SL_PREV"
rmdir "$CLAUDE_DIR/statusline" 2>/dev/null || true
echo "  removed hooks, skill, channel, and statusline"
echo "  left the host's providers in place (clients/providers/install.sh --uninstall removes them)"
echo "  left ~/.config/agistry/client.env in place (delete it yourself if you want)"
echo "Done."
