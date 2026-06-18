#!/usr/bin/env bash
# Records this Claude Code session's role in the agistry registry.
# Usage: assign.sh <role> [task] [handle]
#   role   - implementer | reviewer | researcher | planner | architect | ...
#   task   - e.g. TASK-20260618-1234 (optional; inferred by caller from the worktree)
#   handle - a push URL the registry can POST to wake this session (optional)
set -euo pipefail

ROLE="${1:?usage: assign.sh <role> [task] [handle]}"
TASK="${2:-}"
HANDLE="${3:-}"

[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
URL="${AGISTRY_URL:-http://127.0.0.1:7070}"
TOK="${AGISTRY_TOKEN:-}"
SID="${CLAUDE_CODE_SESSION_ID:?CLAUDE_CODE_SESSION_ID is not set — run inside a Claude Code session}"

if command -v jq >/dev/null 2>&1; then
  body="$(jq -nc --arg s "$SID" --arg t "$TASK" --arg r "$ROLE" --arg h "$HANDLE" \
    '{session_id:$s,task:$t,role:$r,handle:$h}')"
else
  body="{\"session_id\":\"$SID\",\"task\":\"$TASK\",\"role\":\"$ROLE\",\"handle\":\"$HANDLE\"}"
fi

curl -sf --max-time 5 -H "X-Registry-Token: $TOK" "$URL/assign" -d "$body"
echo
