# agistry × Claude Code

Make every Claude Code session auto-join the agistry registry, and let an agent
declare its role autonomously.

Two pieces:

| Piece | Type | What it does |
| --- | --- | --- |
| `hooks/agistry-register.sh` | SessionStart hook | Registers an identity stub (session id + cwd + host); nudges the agent to declare its role. |
| `hooks/agistry-deregister.sh` | SessionEnd hook | Marks the session `gone` when it ends. |
| `skills/agistry-join/` | Skill | The agent invokes this **autonomously** to record its task + role. |

The hook can only know *identity* (it fires before any conversation); *role* is
semantic, so the agent declares it via the skill. The hook seeds the trigger by
injecting a nudge into the session's context.

## Configure

Create `~/.config/agistry/client.env` (keep it `0600` — it holds the token):

```bash
mkdir -p ~/.config/agistry
cat > ~/.config/agistry/client.env <<'EOF'
AGISTRY_URL=http://YOUR_HOST:7070
AGISTRY_TOKEN=your-registry-token
EOF
chmod 600 ~/.config/agistry/client.env
```

## Install

```bash
# hooks
mkdir -p ~/.claude/hooks
cp hooks/agistry-register.sh hooks/agistry-deregister.sh ~/.claude/hooks/
chmod +x ~/.claude/hooks/agistry-*.sh

# skill
mkdir -p ~/.claude/skills/agistry-join
cp skills/agistry-join/SKILL.md skills/agistry-join/assign.sh ~/.claude/skills/agistry-join/
chmod +x ~/.claude/skills/agistry-join/assign.sh
```

Then add the hooks to `~/.claude/settings.json` (merge with any existing `hooks`):

```json
{
  "hooks": {
    "SessionStart": [
      { "matcher": "startup",
        "hooks": [{ "type": "command", "command": "$HOME/.claude/hooks/agistry-register.sh" }] }
    ],
    "SessionEnd": [
      { "matcher": "*",
        "hooks": [{ "type": "command", "command": "$HOME/.claude/hooks/agistry-deregister.sh" }] }
    ]
  }
}
```

Being in `~/.claude/settings.json` (user scope), this applies to **every** Claude
Code session on the machine. Start a new session and it appears in the dashboard;
when the agent learns its role it calls `agistry-join` and the row fills in.

## Dependencies

`curl` (required). `jq` (optional — used for robust JSON building; the scripts fall
back to string interpolation without it).
