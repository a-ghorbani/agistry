#!/usr/bin/env bash
# agistry heartbeat daemon. Keeps this session present in the registry while the
# Claude process is alive — regardless of activity — so an idle session persists
# (days is fine). Started detached by the SessionStart hook; stopped by SessionEnd.
# Also self-exits and marks the session gone if the watched Claude process dies.
#
# Each tick it RECONCILES: it replays this session's full desired identity (from the
# local state file the hook + join skill maintain) into the registry, so the session
# self-heals if the registry was wiped or lost it. The replay also proves liveness, so
# it doubles as the heartbeat. Falls back to a bare /heartbeat if there is no state file.
#
# Args: <session_id> <claude_pid>
set -u

SID="${1:?session_id required}"
WATCH="${2:-}"
INTERVAL="${AGISTRY_HEARTBEAT_SEC:-120}"

[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
URL="${AGISTRY_URL:-http://127.0.0.1:7070}"
TOK="${AGISTRY_TOKEN:-}"
STATE_DIR="${AGISTRY_STATE_DIR:-$HOME/.config/agistry/state}"
STATE_FILE="$STATE_DIR/$SID.json"
CONFLICT_FILE="$STATE_DIR/$SID.conflict"

ping() { # $1 = endpoint — bare liveness/identity-less fallback
  curl -sf --max-time 3 -H "X-Registry-Token: $TOK" "$URL/$1" \
    -d "{\"session_id\":\"$SID\"}" >/dev/null 2>&1 || true
}

reconcile() {
  if [ ! -f "$STATE_FILE" ] || ! command -v jq >/dev/null 2>&1; then
    ping heartbeat; return   # no desired-state to replay — just prove liveness
  fi
  local role task cwd host body code
  role="$(jq -r '.role // ""' "$STATE_FILE" 2>/dev/null)" || { ping heartbeat; return; }
  task="$(jq -r '.task // ""' "$STATE_FILE" 2>/dev/null)"
  cwd="$(jq -r '.cwd // ""' "$STATE_FILE" 2>/dev/null)"
  host="$(jq -r '.host // ""' "$STATE_FILE" 2>/dev/null)"
  if [ -n "$role" ]; then
    # replay the full identity — idempotent re-assign of the same role:task is a no-op
    # server-side; only a different live holder makes it conflict.
    body="$(jq -nc --arg s "$SID" --arg t "$task" --arg r "$role" --arg c "$cwd" --arg h "$host" '{session_id:$s,task:$t,role:$r,cwd:$c,host:$h}')"
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 -H "X-Registry-Token: $TOK" "$URL/assign" -d "$body" 2>/dev/null)"
    if [ "$code" = "409" ]; then
      # lost the (task,role) race / identity conflict — surface to the agent instead of
      # looping silently (else the session believes it is registered but is invisible).
      printf 'agistry: could not claim "%s:%s" — already held by another session. Re-pick your role/task and re-run join.\n' "$task" "$role" > "$CONFLICT_FILE" 2>/dev/null || true
    elif [ -n "$code" ] && [ "${code#2}" != "$code" ]; then
      rm -f "$CONFLICT_FILE" 2>/dev/null || true   # success — clear any stale conflict
    fi
  else
    # no role declared yet — keep the stub alive (re-creates it if the registry was wiped)
    body="$(jq -nc --arg s "$SID" --arg c "$cwd" --arg h "$host" '{session_id:$s,cwd:$c,host:$h}')"
    curl -sf --max-time 3 -H "X-Registry-Token: $TOK" "$URL/register" -d "$body" >/dev/null 2>&1 || true
  fi
}

while :; do
  if [ -n "$WATCH" ] && ! kill -0 "$WATCH" 2>/dev/null; then
    ping deregister   # Claude is gone — leave the party immediately
    break
  fi
  reconcile
  sleep "$INTERVAL"
done
