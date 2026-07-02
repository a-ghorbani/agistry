#!/usr/bin/env bash
# agistry Claude Code status line: reflect THIS session's agistry registration in
# the TUI status bar. Claude Code invokes it on each render with a JSON blob on
# stdin ({session_id, workspace.current_dir, ...}); whatever it prints to stdout is
# shown in the status bar.
#
# agistry's register hook / skill write per-session state to
# $AGISTRY_STATE_DIR/<session_id>.json (default ~/.config/agistry/state), gaining
# "role" and "task" fields once the session has joined. This reads that file so the
# status bar always mirrors what the agent registered:
#   joined      -> "🏷  role · task"
#   not joined  -> "📁 <dir> · (not joined)"
set -uo pipefail

input="$(cat)"
sid=""
cwd=""
if command -v jq >/dev/null 2>&1; then
  sid="$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)"
  cwd="$(printf '%s' "$input" | jq -r '.workspace.current_dir // .cwd // empty' 2>/dev/null)"
fi

state_dir="${AGISTRY_STATE_DIR:-$HOME/.config/agistry/state}"
state_file="$state_dir/$sid.json"

role=""
task=""
if [ -n "$sid" ] && [ -f "$state_file" ] && command -v jq >/dev/null 2>&1; then
  role="$(jq -r '.role // empty' "$state_file" 2>/dev/null)"
  task="$(jq -r '.task // empty' "$state_file" 2>/dev/null)"
fi

if [ -n "$role" ] && [ -n "$task" ]; then
  printf '🏷  %s · %s' "$role" "$task"
elif [ -n "$role" ]; then
  printf '🏷  %s' "$role"
elif [ -n "$task" ]; then
  printf '🏷  %s' "$task"
elif [ -n "$cwd" ]; then
  printf '📁 %s · (not joined)' "$(basename "$cwd")"
fi
