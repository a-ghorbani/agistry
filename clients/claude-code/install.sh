#!/usr/bin/env bash
# Installs the agistry Claude Code client (hooks + skill, optionally the channel)
# into ~/.claude, and idempotently wires the SessionStart/SessionEnd hooks into
# ~/.claude/settings.json. Safe to re-run. Resolves its own location, so it works
# from any directory.
#
# Usage:
#   ./install.sh [--url http://HOST:7070] [--token TOKEN] [--with-channel]
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
CFG="$HOME/.config/agistry/client.env"

URL=""; TOKEN=""; WITH_CHANNEL=0
while [ $# -gt 0 ]; do
  case "$1" in
    --url) URL="${2:?}"; shift 2 ;;
    --token) TOKEN="${2:?}"; shift 2 ;;
    --with-channel) WITH_CHANNEL=1; shift ;;
    -h|--help) sed -n '2,11p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

echo "Installing agistry client into $CLAUDE_DIR"

# 1. hooks
mkdir -p "$CLAUDE_DIR/hooks"
install -m 0755 "$HERE/hooks/agistry-register.sh"   "$CLAUDE_DIR/hooks/agistry-register.sh"
install -m 0755 "$HERE/hooks/agistry-deregister.sh" "$CLAUDE_DIR/hooks/agistry-deregister.sh"
echo "  hooks  -> $CLAUDE_DIR/hooks/"

# 2. skill
mkdir -p "$CLAUDE_DIR/skills/agistry"
install -m 0644 "$HERE/skills/agistry/SKILL.md"   "$CLAUDE_DIR/skills/agistry/SKILL.md"
install -m 0755 "$HERE/skills/agistry/agistry.sh" "$CLAUDE_DIR/skills/agistry/agistry.sh"
echo "  skill  -> $CLAUDE_DIR/skills/agistry/"

# 3. config (only written if --url/--token given, or if missing)
if [ -n "$URL" ] || [ -n "$TOKEN" ]; then
  mkdir -p "$(dirname "$CFG")"
  ( umask 077
    { [ -n "$URL" ]   && echo "AGISTRY_URL=$URL";   true
      [ -n "$TOKEN" ] && echo "AGISTRY_TOKEN=$TOKEN"; true; } > "$CFG" )
  chmod 600 "$CFG"
  echo "  config -> $CFG"
elif [ ! -f "$CFG" ]; then
  echo "  config -> NONE YET. Create $CFG with AGISTRY_URL + AGISTRY_TOKEN (or re-run with --url/--token)."
fi

# 4. wire hooks into settings.json (idempotent; needs jq)
SETTINGS="$CLAUDE_DIR/settings.json"
REG="$CLAUDE_DIR/hooks/agistry-register.sh"
DEREG="$CLAUDE_DIR/hooks/agistry-deregister.sh"
if command -v jq >/dev/null 2>&1; then
  [ -f "$SETTINGS" ] || echo '{}' > "$SETTINGS"
  cp "$SETTINGS" "$SETTINGS.bak.$(date +%s)"
  tmp="$(mktemp)"
  jq --arg reg "$REG" --arg dereg "$DEREG" '
    .hooks = (.hooks // {})
    | .hooks.SessionStart = (((.hooks.SessionStart // [])
        | map(select((.hooks // []) | map(.command) | index($reg) | not)))
        + [{matcher:"startup", hooks:[{type:"command", command:$reg}]}])
    | .hooks.SessionEnd = (((.hooks.SessionEnd // [])
        | map(select((.hooks // []) | map(.command) | index($dereg) | not)))
        + [{matcher:"*", hooks:[{type:"command", command:$dereg}]}])
  ' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
  echo "  hooks wired into $SETTINGS (backup saved)"
else
  echo "  jq not found — add the SessionStart/SessionEnd hooks to $SETTINGS by hand (see README)."
fi

# 5. optional channel (live-wake). Deliberately NOT added to mcpServers — it must
#    only run via the explicit launch flag, or it would drain inboxes silently.
if [ "$WITH_CHANNEL" = 1 ]; then
  CH="$CLAUDE_DIR/agistry-channel"
  mkdir -p "$CH"
  install -m 0755 "$HERE/channel/agistry-channel.mjs" "$CH/agistry-channel.mjs"
  install -m 0644 "$HERE/channel/package.json"        "$CH/package.json"
  if command -v npm >/dev/null 2>&1; then
    ( cd "$CH" && npm install --no-audit --no-fund >/dev/null 2>&1 ) && echo "  channel -> $CH (deps installed)" \
      || echo "  channel -> $CH (npm install FAILED — run 'npm install' there)"
  else
    echo "  channel -> $CH (npm not found — install Node deps there manually)"
  fi
  echo
  echo "  To enable live-wake, launch Claude Code with the channel flag (do NOT add it to mcpServers):"
  echo "    claude --dangerously-load-development-channels server:$CH/agistry-channel.mjs"
  echo "  Tip: alias it, e.g.  alias claude-party='claude --dangerously-load-development-channels server:$CH/agistry-channel.mjs'"
fi

echo
echo "Done. New Claude Code sessions will auto-register. Open a fresh session to test."
