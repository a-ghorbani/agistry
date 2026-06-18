#!/usr/bin/env bash
# agistry heartbeat daemon. Keeps this session present in the registry while the
# Claude process is alive — regardless of activity — so an idle session persists
# (days is fine). Started detached by the SessionStart hook; stopped by SessionEnd.
# Also self-exits and marks the session gone if the watched Claude process dies, so
# there are no zombies.
#
# Args: <session_id> <claude_pid>
set -u

SID="${1:?session_id required}"
WATCH="${2:-}"
INTERVAL="${AGISTRY_HEARTBEAT_SEC:-120}"

[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
URL="${AGISTRY_URL:-http://127.0.0.1:7070}"
TOK="${AGISTRY_TOKEN:-}"

ping() { # $1 = endpoint
  curl -sf --max-time 3 -H "X-Registry-Token: $TOK" "$URL/$1" \
    -d "{\"session_id\":\"$SID\"}" >/dev/null 2>&1 || true
}

while :; do
  if [ -n "$WATCH" ] && ! kill -0 "$WATCH" 2>/dev/null; then
    ping deregister   # Claude is gone — leave the party immediately
    break
  fi
  ping heartbeat
  sleep "$INTERVAL"
done
