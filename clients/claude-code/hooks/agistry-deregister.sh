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

# Stop the heartbeat first: SessionEnd hooks get little time, and a heartbeat left
# running would keep replaying this session's role, so after /clear the next session
# could not claim it.
PIDFILE="${TMPDIR:-/tmp}/agistry-hb-$SID.pid"
if [ -f "$PIDFILE" ]; then
  kill "$(cat "$PIDFILE" 2>/dev/null)" 2>/dev/null || true
  rm -f "$PIDFILE"
fi

# Drop the channel pointer that names this session. On /clear the process lives on
# and the next SessionStart reads the pointer to carry the role over, so keep it.
if [ "$(jget .reason)" != "clear" ]; then
  for ptr in "${AGISTRY_STATE_DIR:-$HOME/.config/agistry/state}"/by-pid/*; do
    [ -f "$ptr" ] && [ "$(cut -f1 "$ptr" 2>/dev/null)" = "$SID" ] && rm -f "$ptr"
  done
fi

curl -sf --max-time 3 -H "X-Registry-Token: $TOK" "$URL/deregister" \
  -d "{\"session_id\":\"$SID\"}" >/dev/null 2>&1 || true
exit 0
