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

# Skip subagents — only top-level interactive sessions join the party.
ATYPE="$(jget .agent_type)"
[ -n "$ATYPE" ] && exit 0
[ "${CLAUDE_CODE_CHILD_SESSION:-}" = "true" ] && exit 0

SID="$(jget .session_id)"; [ -z "$SID" ] && SID="${CLAUDE_CODE_SESSION_ID:-}"
CWD="$(jget .cwd)"; [ -z "$CWD" ] && CWD="$PWD"
[ -z "$SID" ] && exit 0

HOST="$(hostname 2>/dev/null || echo unknown)"
if have_jq; then
  body="$(jq -nc --arg s "$SID" --arg c "$CWD" --arg h "$HOST" '{session_id:$s,cwd:$c,host:$h}')"
else
  body="{\"session_id\":\"$SID\",\"cwd\":\"$CWD\",\"host\":\"$HOST\"}"
fi

curl -sf --max-time 3 -H "X-Registry-Token: $TOK" "$URL/register" -d "$body" >/dev/null 2>&1 || true

# SessionStart stdout is injected into the agent's context — seed the role-register trigger.
cat <<NUDGE
[agistry] This session ($SID) joined the agent registry at $URL.
As soon as it is clear what your task is, register yourself to the registry with your clear task and role by using the agistry skill (run: ~/.claude/skills/agistry/agistry.sh join <role> <task>).
You can also use the agistry skill to see who else is working (who) and to message or hand off to other agents (send/inbox).
NUDGE
exit 0
