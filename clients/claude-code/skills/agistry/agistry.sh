#!/usr/bin/env bash
# agistry CLI — a thin, authenticated wrapper over the agistry registry API for use
# from a Claude Code session. Reads the registry URL + token from
# ~/.config/agistry/client.env and this session's id from $CLAUDE_CODE_SESSION_ID,
# so callers never handle the token directly.
#
# Usage:
#   agistry.sh join <role> [task-tag] [--force]  # declare THIS session's role -> /assign
#   agistry.sh who [task] [role]                 # list agents                 -> /agents
#   agistry.sh send <to> <msg>                   # message TASK:role or session -> /send
#   agistry.sh inbox                             # drain THIS session's mailbox -> /inbox
#   agistry.sh heartbeat                         # keep THIS session alive       -> /heartbeat
#   agistry.sh register [cwd]                    # identity stub (hook does this) -> /register
#   agistry.sh leave                             # mark THIS session gone         -> /deregister
set -uo pipefail

[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
URL="${AGISTRY_URL:-http://127.0.0.1:7070}"
TOK="${AGISTRY_TOKEN:-}"
SID="${CLAUDE_CODE_SESSION_ID:-}"
STATE_DIR="${AGISTRY_STATE_DIR:-$HOME/.config/agistry/state}"
AUTH=(-H "X-Registry-Token: $TOK")

# Surface a conflict the background reconciler hit (e.g. it lost the race for your
# role:task) the next time the agent runs any agistry command, then clear it.
if [ -n "$SID" ] && [ -f "$STATE_DIR/$SID.conflict" ]; then
  cat "$STATE_DIR/$SID.conflict" >&2
  rm -f "$STATE_DIR/$SID.conflict" 2>/dev/null || true
fi

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

# Stable content-derived message id so a retried send dedupes (server is INSERT OR
# IGNORE on msg_id). Identical messages to the same target collapse to one — fine for
# handoffs. Empty if no hasher is available; the server then mints one.
content_id() { # $1=to $2=msg
  printf '%s' "$SID|$1|$2" | { sha1sum 2>/dev/null || shasum 2>/dev/null || md5sum 2>/dev/null || true; } | cut -c1-20
}

# Persist task/role into the local desired-state file (atomic, merge-preserving) so the
# heartbeat daemon keeps reconciling this identity even across a registry wipe.
write_state() { # $1=task $2=role
  command -v jq >/dev/null 2>&1 || return 0
  local f="$STATE_DIR/$SID.json" tmp base='{}'
  mkdir -p "$STATE_DIR" 2>/dev/null || return 0
  tmp="$(mktemp "$STATE_DIR/.tmp.XXXXXX" 2>/dev/null)" || return 0
  [ -f "$f" ] && base="$(jq -c . "$f" 2>/dev/null || echo '{}')"
  printf '%s' "$base" | jq -c --arg s "$SID" --arg t "$1" --arg r "$2" \
    '.session_id=$s | .task=$t | .role=$r' > "$tmp" 2>/dev/null && mv -f "$tmp" "$f" || rm -f "$tmp"
}

cmd="${1:-help}"; shift || true
case "$cmd" in
  register)
    need_sid; post /register "$(jobj session_id="$SID" cwd="${1:-$PWD}" host="$(hostname 2>/dev/null || echo unknown)")" ;;
  join|assign)
    need_sid
    role="${1:?role required}"; task="${2:-}"; force=""
    case "${3:-}" in --force|force) force=1 ;; esac
    body="$(jobj session_id="$SID" role="$role" task="$task" cwd="$PWD" host="$(hostname 2>/dev/null || echo unknown)")"
    # force is a JSON boolean, not a string — inject it raw (jobj's --arg quotes values)
    [ -n "$force" ] && body="${body%\}},\"force\":true}"
    resp="$(curl -s --max-time 5 "${AUTH[@]}" "$URL/assign" -d "$body")"
    printf '%s\n' "$resp"
    # persist desired-state for the reconciling daemon ONLY on confirmed success, so a
    # rejected change never poisons the local state the daemon replays.
    case "$resp" in *'"status":"assigned"'*) write_state "$task" "$role" ;; esac ;;
  who|agents)
    params=()
    [ -n "${1:-}" ] && params+=("task=$1")
    [ -n "${2:-}" ] && params+=("role=$2")
    qs=""; [ ${#params[@]} -gt 0 ] && qs="?$(IFS='&'; echo "${params[*]}")"
    get "/agents$qs" ;;
  send)
    need_sid
    to="${1:?target required: TASK:role or session_id}"; msg="${2:?message required}"
    post /send "$(jobj to="$to" from="$SID" msg="$msg" msg_id="$(content_id "$to" "$msg")")" ;;
  inbox)
    need_sid; get "/inbox?session_id=$SID" ;;
  heartbeat)
    need_sid; post /heartbeat "$(jobj session_id="$SID")" ;;
  leave|deregister)
    need_sid; post /deregister "$(jobj session_id="$SID")" ;;
  *)
    sed -n '2,20p' "$0" ;;
esac
