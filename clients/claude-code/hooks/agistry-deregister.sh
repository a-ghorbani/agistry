#!/usr/bin/env bash
# agistry SessionEnd hook: marks this session 'gone' in the registry. Best-effort;
# never blocks. (The registry's TTL reaper also covers sessions that never fire this.)
set -u

input="$(cat)"
[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
URL="${AGISTRY_URL:-http://127.0.0.1:7070}"
TOK="${AGISTRY_TOKEN:-}"

have_jq() { command -v jq >/dev/null 2>&1; }
jget() { have_jq && printf '%s' "$input" | jq -r "$1 // empty" 2>/dev/null; }

SID="$(jget .session_id)"; [ -z "$SID" ] && SID="${CLAUDE_CODE_SESSION_ID:-}"
[ -z "$SID" ] && exit 0

curl -sf --max-time 3 -H "X-Registry-Token: $TOK" "$URL/deregister" \
  -d "{\"session_id\":\"$SID\"}" >/dev/null 2>&1 || true

# stop the heartbeat daemon for this session
PIDFILE="${TMPDIR:-/tmp}/agistry-hb-$SID.pid"
if [ -f "$PIDFILE" ]; then
  kill "$(cat "$PIDFILE" 2>/dev/null)" 2>/dev/null || true
  rm -f "$PIDFILE"
fi
exit 0
