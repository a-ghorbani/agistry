#!/usr/bin/env bash
# agistry CLI — a thin, authenticated wrapper over the agistry registry API for use
# from a Claude Code session. Reads the registry URL + token from
# ~/.config/agistry/client.env and this session's id from $CLAUDE_CODE_SESSION_ID,
# so callers never handle the token directly.
#
# Usage:
#   agistry.sh join <role> [task] [handle]   # declare THIS session's role  -> /assign
#   agistry.sh who [task] [role]             # list agents                  -> /agents
#   agistry.sh send <to> <msg>               # message TASK:role or session -> /send
#   agistry.sh inbox                         # drain THIS session's mailbox -> /inbox
#   agistry.sh heartbeat                     # keep THIS session alive       -> /heartbeat
#   agistry.sh register [cwd]                # identity stub (hook does this) -> /register
#   agistry.sh leave                         # mark THIS session gone         -> /deregister
set -uo pipefail

[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
URL="${AGISTRY_URL:-http://127.0.0.1:7070}"
TOK="${AGISTRY_TOKEN:-}"
SID="${CLAUDE_CODE_SESSION_ID:-}"
AUTH=(-H "X-Registry-Token: $TOK")

post() { curl -s --max-time 5 "${AUTH[@]}" "$URL$1" -d "$2"; echo; }
get()  { curl -s --max-time 5 "${AUTH[@]}" "$URL$1"; echo; }

# Build a JSON object from key=value args (jq if present, naive fallback otherwise).
jobj() {
  if command -v jq >/dev/null 2>&1; then
    local args=() filter='{' first=1 k v
    for kv in "$@"; do
      k="${kv%%=*}"; v="${kv#*=}"; args+=(--arg "$k" "$v")
      [ $first -eq 1 ] || filter+=','
      filter+="$k:\$$k"; first=0
    done
    jq -nc "${args[@]}" "$filter}"
  else
    local out='{' first=1 k v
    for kv in "$@"; do
      k="${kv%%=*}"; v="${kv#*=}"
      [ $first -eq 1 ] || out+=','
      out+="\"$k\":\"$v\""; first=0
    done
    echo "$out}"
  fi
}

need_sid() { [ -n "$SID" ] || { echo '{"error":"CLAUDE_CODE_SESSION_ID not set"}'; exit 1; }; }

cmd="${1:-help}"; shift || true
case "$cmd" in
  register)
    need_sid; post /register "$(jobj session_id="$SID" cwd="${1:-$PWD}" host="$(hostname 2>/dev/null || echo unknown)")" ;;
  join|assign)
    need_sid; post /assign "$(jobj session_id="$SID" role="${1:?role required}" task="${2:-}" handle="${3:-}")" ;;
  who|agents)
    params=()
    [ -n "${1:-}" ] && params+=("task=$1")
    [ -n "${2:-}" ] && params+=("role=$2")
    qs=""; [ ${#params[@]} -gt 0 ] && qs="?$(IFS='&'; echo "${params[*]}")"
    get "/agents$qs" ;;
  send)
    need_sid; post /send "$(jobj to="${1:?target required: TASK:role or session_id}" from="$SID" msg="${2:?message required}")" ;;
  inbox)
    need_sid; get "/inbox?session_id=$SID" ;;
  heartbeat)
    need_sid; post /heartbeat "$(jobj session_id="$SID")" ;;
  leave|deregister)
    need_sid; post /deregister "$(jobj session_id="$SID")" ;;
  *)
    sed -n '2,20p' "$0" ;;
esac
