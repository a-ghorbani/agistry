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
#
# If the profile already had a status line, the installer saves it next to this
# script as agistry-statusline.prev.json; it is run with the same input and its
# output is shown first, with ours appended: "<previous> │ 🏷  role · task".
set -uo pipefail

input="$(cat)"

prev=""
prev_file="$(dirname "${BASH_SOURCE[0]}")/agistry-statusline.prev.json"
if [ -f "$prev_file" ] && command -v jq >/dev/null 2>&1; then
  # agistry_replaced: an older copy of this script, kept only so uninstall restores it
  prev_cmd="$(jq -r 'if .agistry_replaced then empty else .command // empty end' "$prev_file" 2>/dev/null)"
  [ -n "$prev_cmd" ] && prev="$(printf '%s' "$input" | bash -c "$prev_cmd" 2>/dev/null)"
fi
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

ours=""
if [ -n "$role" ] && [ -n "$task" ]; then
  ours="$(printf '🏷  %s · %s' "$role" "$task")"
elif [ -n "$role" ]; then
  ours="🏷  $role"
elif [ -n "$task" ]; then
  ours="🏷  $task"
elif [ -n "$cwd" ]; then
  ours="📁 $(basename "$cwd") · (not joined)"
fi

if [ -n "$prev" ] && [ -n "$ours" ]; then
  printf '%s │ %s' "$prev" "$ours"
else
  printf '%s' "$prev$ours"
fi
