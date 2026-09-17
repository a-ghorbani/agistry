#!/usr/bin/env bash
# agistry SessionStart hook: registers an identity stub for this Claude Code
# session (session_id + cwd + host) and nudges the agent to declare its role via
# the agistry-join skill. Never blocks or fails the session.
#
# Config (optional): ~/.config/agistry/client.env may set AGISTRY_URL / AGISTRY_TOKEN.
set -u

input="$(cat)"
[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
URL="${AGISTRY_URL:-http://127.0.0.1:7070}"
TOK="${AGISTRY_TOKEN:-}"

have_jq() { command -v jq >/dev/null 2>&1; }
jget() { have_jq && printf '%s' "$input" | jq -r "$1 // empty" 2>/dev/null; }

# Persist this session's desired identity locally so the heartbeat daemon can keep
# reconciling it into the registry (self-heals after a registry wipe). Written
# atomically (temp + rename); merges so a later `join` can add task/role.
STATE_DIR="${AGISTRY_STATE_DIR:-$HOME/.config/agistry/state}"
write_state_stub() { # $1=session_id $2=cwd $3=host
  local dir="$STATE_DIR" f tmp
  mkdir -p "$dir" 2>/dev/null || return 0
  f="$dir/$1.json"
  tmp="$(mktemp "$dir/.tmp.XXXXXX" 2>/dev/null)" || return 0
  if have_jq; then
    local base='{}'; [ -f "$f" ] && base="$(jq -c . "$f" 2>/dev/null || echo '{}')"
    printf '%s' "$base" | jq -c --arg s "$1" --arg c "$2" --arg h "$3" \
      '.session_id=$s | .cwd=$c | .host=$h' > "$tmp" 2>/dev/null && mv -f "$tmp" "$f" || rm -f "$tmp"
  else
    printf '{"session_id":"%s","cwd":"%s","host":"%s"}\n' "$1" "$2" "$3" > "$tmp" && mv -f "$tmp" "$f" || rm -f "$tmp"
  fi
}

# Skip subagents — only top-level interactive sessions join the party.
ATYPE="$(jget .agent_type)"
[ -n "$ATYPE" ] && exit 0
[ "${CLAUDE_CODE_CHILD_SESSION:-}" = "true" ] && exit 0

SID="$(jget .session_id)"; [ -z "$SID" ] && SID="${CLAUDE_CODE_SESSION_ID:-}"
SOURCE="$(jget .source)"
CWD="$(jget .cwd)"; [ -z "$CWD" ] && CWD="$PWD"
[ -z "$SID" ] && exit 0

HOST="$(hostname 2>/dev/null || echo unknown)"
write_state_stub "$SID" "$CWD" "$HOST"
if have_jq; then
  body="$(jq -nc --arg s "$SID" --arg c "$CWD" --arg h "$HOST" '{session_id:$s,cwd:$c,host:$h}')"
else
  body="{\"session_id\":\"$SID\",\"cwd\":\"$CWD\",\"host\":\"$HOST\"}"
fi

curl -sf --max-time 3 -H "X-Registry-Token: $TOK" "$URL/register" -d "$body" >/dev/null 2>&1 || true

# Find the Claude process that owns this session: walk up past shells to the first
# process named claude, else the first non-shell ancestor. The heartbeat watches it,
# and the channel (a child of the same process) finds this session through it.
cpid=""; nonshell=""; p="$PPID"
for _ in 1 2 3 4 5 6 7 8; do
  { [ -z "$p" ] || [ "$p" -le 1 ] 2>/dev/null; } && break
  c=$(ps -o comm= -p "$p" 2>/dev/null); c=${c##*/}
  if [ "$c" = "claude" ]; then cpid="$p"; break; fi
  case "$c" in sh|-sh|bash|-bash|zsh|-zsh|dash|fish) : ;; *) [ -z "$nonshell" ] && nonshell="$p" ;; esac
  p=$(ps -o ppid= -p "$p" 2>/dev/null | tr -d ' ')
done
[ -z "$cpid" ] && cpid="${nonshell:-$PPID}"

# Point the channel at this session. A channel reads the session id from its env once,
# but a picker `claude --resume` spawns it under a throwaway id and /clear mints a new
# one, so it would poll the wrong inbox. by-pid/<claude pid> holds "<sid>\t<start time>";
# the start time lets a reader reject a file left behind for a recycled pid.
BYPID="$STATE_DIR/by-pid"
PTR="$BYPID/$cpid"
start="$(LC_ALL=C TZ=UTC ps -o lstart= -p "$cpid" 2>/dev/null | tr -s ' ' | sed 's/^ //;s/ $//')"
prev_sid=""
# the previous session of this same process (a file for a recycled pid won't match)
[ -n "$start" ] && [ -f "$PTR" ] && [ "$(cut -f2 "$PTR" 2>/dev/null)" = "$start" ] \
  && prev_sid="$(cut -f1 "$PTR" 2>/dev/null)"
if mkdir -p -m 700 "$BYPID" 2>/dev/null && chmod 700 "$BYPID" 2>/dev/null; then
  if [ -n "$start" ] && tmp="$(mktemp "$BYPID/.tmp.XXXXXX" 2>/dev/null)"; then
    printf '%s\t%s\n' "$SID" "$start" > "$tmp" && mv -f "$tmp" "$PTR" || rm -f "$tmp"
  fi
fi

# /clear ends the old session and starts this one in the same process. Carry the
# declared task/role over so TASK:role mail keeps arriving without a re-join; the
# heartbeat replays it into the registry. (Mail addressed to the old session id stays
# with that id.)
if [ "$SOURCE" = "clear" ] && [ -n "$prev_sid" ] && [ "$prev_sid" != "$SID" ] && have_jq; then
  # in case SessionEnd was cut short: the old heartbeat would keep holding the role
  oldpf="${TMPDIR:-/tmp}/agistry-hb-$prev_sid.pid"
  [ -f "$oldpf" ] && { kill "$(cat "$oldpf" 2>/dev/null)" 2>/dev/null; rm -f "$oldpf"; }
  prev="$STATE_DIR/$prev_sid.json" cur="$STATE_DIR/$SID.json"
  if [ -f "$prev" ] && [ -f "$cur" ] && [ -n "$(jq -r '.role // empty' "$prev" 2>/dev/null)" ] \
     && tmp="$(mktemp "$STATE_DIR/.tmp.XXXXXX" 2>/dev/null)"; then
    jq -c --slurpfile p "$prev" '. + {task: $p[0].task, role: $p[0].role}' "$cur" > "$tmp" 2>/dev/null \
      && mv -f "$tmp" "$cur" || rm -f "$tmp"
  fi
fi

# Start the heartbeat daemon so this session stays present while Claude is alive,
# even when idle.
HB="$HOME/.claude/hooks/agistry-heartbeat.sh"
PIDFILE="${TMPDIR:-/tmp}/agistry-hb-$SID.pid"
if [ -x "$HB" ] && { [ ! -f "$PIDFILE" ] || ! kill -0 "$(cat "$PIDFILE" 2>/dev/null)" 2>/dev/null; }; then
  nohup "$HB" "$SID" "$cpid" >/dev/null 2>&1 &
  echo $! > "$PIDFILE"
fi

# Optionally start this host's resource providers. Two gates, both default off, so a
# machine with no devices attached (the common case) starts no extra process: the host
# must both name providers in AGISTRY_PROVIDERS and opt into session autostart. A
# pidfile guard inside the launcher keeps it to one daemon per provider per host no
# matter how many sessions call it. On a host that permanently owns devices, prefer the
# user service in clients/providers/ over this.
if [ -n "${AGISTRY_PROVIDERS:-}" ] && [ "${AGISTRY_PROVIDERS_AUTOSTART:-0}" = "1" ]; then
  PROV="$HOME/.claude/providers/agistry-providers.sh"
  [ -x "$PROV" ] && nohup "$PROV" start >/dev/null 2>&1 &
fi

# SessionStart stdout is injected into the agent's context — seed the role-register trigger.
cat <<NUDGE
[agistry] This session ($SID) joined the agent registry at $URL.
As soon as it is clear what your task is, register yourself to the registry with your clear task and role by using the agistry skill (run: ~/.claude/skills/agistry/agistry.sh join <role> <task>).
You can also use the agistry skill to see who else is working (who) and to message or hand off to other agents (send/inbox).
NUDGE
exit 0
