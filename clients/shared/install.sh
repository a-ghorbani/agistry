#!/usr/bin/env bash
# The parts every agistry client shares, installed once per host. Called by the Codex,
# opencode and pi installers (the Claude Code client keeps its own skill copy per
# profile, but shares the config and providers):
#
#   ~/.config/agistry/client.env         AGISTRY_URL / AGISTRY_TOKEN (only the keys given change)
#   ~/.config/agistry/lib/               the in-process client used by the opencode and pi plugins
#   ~/.agents/skills/agistry/            the agistry skill + CLI, worded for any agent
#   ~/.config/agistry/providers/         the host's device providers (clients/providers/install.sh)
#
# Usage: ./install.sh [--url URL] [--token TOKEN]
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLIENTS="$(dirname "$HERE")"
CFG="$HOME/.config/agistry/client.env"
URL=""; TOKEN=""
while [ $# -gt 0 ]; do
  case "$1" in
    --url) URL="${2:?}"; shift 2 ;;
    --token) TOKEN="${2:?}"; shift 2 ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

# 1. config: replace only the keys given, keep everything else
if [ -n "$URL" ] || [ -n "$TOKEN" ]; then
  mkdir -p "$(dirname "$CFG")"
  ( umask 077
    tmp="$(mktemp)"
    [ -f "$CFG" ] && cat "$CFG" > "$tmp"
    for kv in ${URL:+"AGISTRY_URL=$URL"} ${TOKEN:+"AGISTRY_TOKEN=$TOKEN"}; do
      grep -v "^\(export \)\{0,1\}${kv%%=*}=" "$tmp" > "$tmp.new" || true
      mv "$tmp.new" "$tmp"; echo "$kv" >> "$tmp"
    done
    mv "$tmp" "$CFG" )
  chmod 600 "$CFG"
  echo "  config -> $CFG"
elif [ ! -f "$CFG" ]; then
  echo "  config -> NONE YET. Create $CFG with AGISTRY_URL + AGISTRY_TOKEN (or re-run with --url/--token)."
fi

# 2. in-process client library
mkdir -p "$HOME/.config/agistry/lib"
install -m 0644 "$HERE/agistry-session.mjs" "$HOME/.config/agistry/lib/agistry-session.mjs"
echo "  library -> ~/.config/agistry/lib/agistry-session.mjs"

# 3. skill, reworded from the Claude Code one for any agent
SKILL="$HOME/.agents/skills/agistry"
SRC="$HERE/skill/agistry"
mkdir -p "$SKILL"
python3 - "$SRC/SKILL.md" "$SKILL/SKILL.md" <<'PY'
import sys
text = open(sys.argv[1]).read()
for old, new in [
    ("This session's SessionStart hook already created an identity stub;",
     "Your agent's agistry integration already registered this session;"),
    ("your session id from `$CLAUDE_CODE_SESSION_ID`",
     "your session id from your agent's environment (`CODEX_THREAD_ID`, `PI_SESSION_ID`, or `AGISTRY_SESSION_ID`)"),
    ("~/.claude/skills/agistry", "~/.agents/skills/agistry"),
    ("A subagent shares its parent's session id", "A subagent may report its parent's session id"),
    ("or immediately if it is running with the live channel.",
     "or as soon as its agent's integration delivers it: a queued follow-up after the current turn, or a new turn if it is idle. Delivered means handed to the agent, not acted on."),
]:
    assert old in text, old
    text = text.replace(old, new)
text += ("\nIf `agistry.sh` reports no session id, prefix every command with "
         "`AGISTRY_SESSION_ID=<id>`, using the session id you were given at session start. "
         "Never guess or reuse another session's id.\n")
open(sys.argv[2], "w").write(text)
PY
install -m 0755 "$SRC/agistry.sh" "$SKILL/agistry.sh"
echo "  skill -> $SKILL"

# 4. device providers (host-wide; started as services for AGISTRY_PROVIDERS)
"$CLIENTS/providers/install.sh"
