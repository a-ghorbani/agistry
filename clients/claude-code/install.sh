#!/usr/bin/env bash
# Installs the agistry Claude Code client (hooks + skill, optionally the channel)
# into ~/.claude, and idempotently wires the SessionStart/SessionEnd hooks into
# ~/.claude/settings.json. Safe to re-run. Resolves its own location, so it works
# from any directory. Set CLAUDE_DIR to install into another profile (the dir you
# point CLAUDE_CONFIG_DIR at), e.g. CLAUDE_DIR=~/.claude-work1 ./install.sh.
#
# Usage:
#   ./install.sh [--url http://HOST:7070] [--token TOKEN] [--with-channel] [--with-statusline]
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
CFG="$HOME/.config/agistry/client.env"
# How the profile is shown to the agent (~-relative), and the env prefix the claude
# CLI needs to act on a non-default profile.
CLAUDE_DIR_SHOWN="${CLAUDE_DIR/#"$HOME"/"~"}"
if [ "$CLAUDE_DIR" = "$HOME/.claude" ]; then CLI_ENV=""; else CLI_ENV="CLAUDE_CONFIG_DIR=$CLAUDE_DIR "; fi
# Run the claude CLI against the target profile. The default profile keeps its user
# config in ~/.claude.json, so it must run with CLAUDE_CONFIG_DIR unset, not pointed
# at ~/.claude.
claude_cli() {
  if [ -n "$CLI_ENV" ]; then CLAUDE_CONFIG_DIR="$CLAUDE_DIR" claude "$@"
  else env -u CLAUDE_CONFIG_DIR claude "$@"; fi
}

URL=""; TOKEN=""; WITH_CHANNEL=0; WITH_STATUSLINE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --url) URL="${2:?}"; shift 2 ;;
    --token) TOKEN="${2:?}"; shift 2 ;;
    --with-channel) WITH_CHANNEL=1; shift ;;
    --with-statusline) WITH_STATUSLINE=1; shift ;;
    -h|--help) sed -n '2,13p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

echo "Installing agistry client into $CLAUDE_DIR"

# 1. hooks
mkdir -p "$CLAUDE_DIR/hooks"
install -m 0755 "$HERE/hooks/agistry-register.sh"   "$CLAUDE_DIR/hooks/agistry-register.sh"
install -m 0755 "$HERE/hooks/agistry-deregister.sh" "$CLAUDE_DIR/hooks/agistry-deregister.sh"
install -m 0755 "$HERE/hooks/agistry-heartbeat.sh"  "$CLAUDE_DIR/hooks/agistry-heartbeat.sh"
echo "  hooks  -> $CLAUDE_DIR/hooks/"

# 2. skill
mkdir -p "$CLAUDE_DIR/skills/agistry"
# SKILL.md names the default profile's script path; point it at this profile's copy.
sed "s|~/\.claude/skills/agistry|$CLAUDE_DIR_SHOWN/skills/agistry|g" "$HERE/skills/agistry/SKILL.md" \
  > "$CLAUDE_DIR/skills/agistry/SKILL.md"
chmod 0644 "$CLAUDE_DIR/skills/agistry/SKILL.md"
install -m 0755 "$HERE/skills/agistry/agistry.sh" "$CLAUDE_DIR/skills/agistry/agistry.sh"
echo "  skill  -> $CLAUDE_DIR/skills/agistry/"

# 2b. resource providers: host-wide, not per profile, so they go to one shared place
#     (inert unless AGISTRY_PROVIDERS names one). Drop the per-profile copy that older
#     installs put here; a provider already running from it keeps running.
"$HERE/../providers/install.sh"
if [ -d "$CLAUDE_DIR/providers" ]; then
  rm -rf "$CLAUDE_DIR/providers"
  echo "  removed old per-profile copy $CLAUDE_DIR/providers"
fi

# 3. config. Shared by every profile, so only the keys given are replaced; the rest
#    (the other key, AGISTRY_PROVIDERS, ...) are kept.
if [ -n "$URL" ] || [ -n "$TOKEN" ]; then
  mkdir -p "$(dirname "$CFG")"
  ( umask 077
    tmp="$(mktemp)"
    [ -f "$CFG" ] && cat "$CFG" > "$tmp"
    if [ -n "$URL" ]; then
      grep -v '^AGISTRY_URL=' "$tmp" > "$tmp.new" || true; mv "$tmp.new" "$tmp"
      echo "AGISTRY_URL=$URL" >> "$tmp"
    fi
    if [ -n "$TOKEN" ]; then
      grep -v '^AGISTRY_TOKEN=' "$tmp" > "$tmp.new" || true; mv "$tmp.new" "$tmp"
      echo "AGISTRY_TOKEN=$TOKEN" >> "$tmp"
    fi
    mv "$tmp" "$CFG" )
  chmod 600 "$CFG"
  echo "  config -> $CFG"
elif [ ! -f "$CFG" ]; then
  echo "  config -> NONE YET. Create $CFG with AGISTRY_URL + AGISTRY_TOKEN (or re-run with --url/--token)."
fi

# 4. wire hooks into settings.json (idempotent; needs jq)
SETTINGS="$CLAUDE_DIR/settings.json"
REG="$CLAUDE_DIR/hooks/agistry-register.sh"
DEREG="$CLAUDE_DIR/hooks/agistry-deregister.sh"
if command -v jq >/dev/null 2>&1; then
  [ -f "$SETTINGS" ] || echo '{}' > "$SETTINGS"
  cp "$SETTINGS" "$SETTINGS.bak.$(date +%s)"
  tmp="$(mktemp)"
  jq --arg reg "$REG" --arg dereg "$DEREG" '
    .hooks = (.hooks // {})
    | .hooks.SessionStart = (((.hooks.SessionStart // [])
        | map(select(([.hooks[]?.command] | map(test("agistry-register")) | any) | not)))
        + [{matcher:"startup|resume|clear", hooks:[{type:"command", command:$reg}]}])
    | .hooks.SessionEnd = (((.hooks.SessionEnd // [])
        | map(select(([.hooks[]?.command] | map(test("agistry-deregister")) | any) | not)))
        + [{matcher:"*", hooks:[{type:"command", command:$dereg}]}])
  ' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
  echo "  hooks wired into $SETTINGS (backup saved)"
else
  echo "  jq not found — add the SessionStart/SessionEnd hooks to $SETTINGS by hand (see README)."
fi

# 5. optional channel (live-wake). Registered as a user-scope MCP server so the
#    --dangerously-load-development-channels flag can find it by name. That makes
#    Claude spawn it in every session, which is safe: it stays idle unless
#    AGISTRY_CHANNEL_ACTIVE=1 is set, which only the claude-party launcher does.
if [ "$WITH_CHANNEL" = 1 ]; then
  CH="$CLAUDE_DIR/agistry-channel"
  mkdir -p "$CH"
  install -m 0755 "$HERE/channel/agistry-channel.mjs" "$CH/agistry-channel.mjs"
  install -m 0644 "$HERE/channel/package.json"        "$CH/package.json"
  if command -v npm >/dev/null 2>&1; then
    ( cd "$CH" && npm install --no-audit --no-fund >/dev/null 2>&1 ) && echo "  channel -> $CH (deps installed)" \
      || echo "  channel -> $CH (npm install FAILED — run 'npm install' there)"
  else
    echo "  channel -> $CH (npm not found — install Node deps there manually)"
  fi
  MCP_ADD="${CLI_ENV}claude mcp add -s user agistry-channel -- node $CH/agistry-channel.mjs"
  if command -v claude >/dev/null 2>&1; then
    # Re-register so a re-run (or a stale path) ends up pointing at this profile's copy.
    claude_cli mcp remove -s user agistry-channel >/dev/null 2>&1 || true
    if claude_cli mcp add -s user agistry-channel -- node "$CH/agistry-channel.mjs" >/dev/null 2>&1; then
      echo "  channel registered as MCP server 'agistry-channel' (user scope)"
    else
      echo "  channel MCP registration FAILED — run it yourself:"
      echo "    $MCP_ADD"
    fi
  else
    echo "  claude CLI not found — register the channel yourself:"
    echo "    $MCP_ADD"
  fi
  PARTY="claude-party"
  [ -n "$CLI_ENV" ] && PARTY="claude-party-$(basename "$CLAUDE_DIR" | sed 's/^\.claude-//; s/^\.//')"
  echo
  echo "  Last step for live-wake: add this launcher to your ~/.zshrc or ~/.bashrc, then"
  echo "  start sessions with '$PARTY' (accepts the usual claude args, e.g. --resume):"
  echo
  echo "    $PARTY() {"
  echo "      ${CLI_ENV}AGISTRY_CHANNEL_ACTIVE=1 claude \"\$@\" \\"
  echo "        --dangerously-load-development-channels server:agistry-channel"
  echo "    }"
  echo
fi

# 6. optional status line. settings.json holds a SINGLE .statusLine, so rather than
#    clobber one the user already has, we save it beside our script and ours runs it
#    and appends to its output. One that already shows agistry state (an older copy
#    of ours) is replaced instead, or the label would show twice.
if [ "$WITH_STATUSLINE" = 1 ]; then
  SL="$CLAUDE_DIR/statusline/agistry-statusline.sh"
  SL_PREV="$CLAUDE_DIR/statusline/agistry-statusline.prev.json"
  mkdir -p "$CLAUDE_DIR/statusline"
  install -m 0755 "$HERE/statusline/agistry-statusline.sh" "$SL"
  echo "  statusline -> $SL"
  if command -v jq >/dev/null 2>&1; then
    [ -f "$SETTINGS" ] || echo '{}' > "$SETTINGS"
    PREV="$(jq -r '.statusLine.command // empty' "$SETTINGS" 2>/dev/null)"
    if [ -n "$PREV" ] && [ "$PREV" != "$SL" ]; then
      PREV_BIN="${PREV%% *}"; PREV_BIN="${PREV_BIN/#\~/$HOME}"
      if [ -f "$PREV_BIN" ] && grep -q 'agistry/state' "$PREV_BIN" 2>/dev/null; then
        # saved only so uninstall can put it back; marked so ours does not run it
        jq '.statusLine + {agistry_replaced: true}' "$SETTINGS" > "$SL_PREV"
        echo "  replacing existing statusLine ($PREV) — it already shows agistry state"
      else
        jq '.statusLine' "$SETTINGS" > "$SL_PREV"
        echo "  combining with existing statusLine ($PREV): shown first, agistry appended"
      fi
    elif [ -z "$PREV" ]; then
      rm -f "$SL_PREV"
    fi  # PREV == SL: a re-run; keep whatever was saved before
    cp "$SETTINGS" "$SETTINGS.bak.$(date +%s)"
    tmp="$(mktemp)"
    # keep the other statusLine fields (e.g. padding)
    jq --arg sl "$SL" '.statusLine = ((.statusLine // {}) + {type:"command", command:$sl})' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
    echo "  statusLine wired into $SETTINGS (backup saved)"
  else
    echo "  jq not found — add this to $SETTINGS by hand:"
    echo "    \"statusLine\": { \"type\": \"command\", \"command\": \"$SL\" }"
  fi
fi

echo
echo "Done. New Claude Code sessions will auto-register. Open a fresh session to test."
