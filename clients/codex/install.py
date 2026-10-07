#!/usr/bin/env python3
"""Idempotent installer; preserves other hooks and shared client configuration."""

import argparse
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import time

from agistry_codex import atomic_json


def backup(path):
    if path.exists():
        shutil.copy2(path, path.with_name(path.name + ".bak." + str(time.time_ns())))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url")
    parser.add_argument("--token")
    args = parser.parse_args()
    os.umask(0o077)
    here = Path(__file__).resolve().parent
    codex = os.environ.get("AGISTRY_CODEX_BIN", "codex")
    # Check capabilities instead of assuming every CLI release exposes queue/proxy.
    for command in ([codex, "queue", "--help"], [codex, "app-server", "proxy", "--help"]):
        subprocess.run(command, check=True, stdout=subprocess.DEVNULL)
    home = Path.home()
    codex_dir = Path(os.environ.get("CODEX_HOME", str(home / ".codex")))
    runtime = codex_dir / "agistry"
    runtime.mkdir(parents=True, exist_ok=True)
    for name in ("agistry-codex.sh", "agistry_codex.py"):
        shutil.copy2(here / name, runtime / name)
        (runtime / name).chmod(0o755)

    skill_dir = home / ".agents/skills/agistry"
    skill_dir.mkdir(parents=True, exist_ok=True)
    original = here.parent / "claude-code/skills/agistry"
    skill = (original / "SKILL.md").read_text()
    skill = skill.replace("This session's SessionStart hook already created an identity stub;",
                          "This session's SessionStart hook records its identity for the local bridge;")
    skill = skill.replace("~/.claude/skills/agistry", "~/.agents/skills/agistry")
    skill = skill.replace("$CLAUDE_CODE_SESSION_ID", "$CODEX_THREAD_ID (or an explicit $AGISTRY_SESSION_ID)")
    skill = skill.replace("A subagent shares its parent's session id", "Hooks can report the parent's session id for a subagent")
    skill = skill.replace("or immediately if it is running with the live channel.",
                          "or as a queued follow-up when the local Codex bridge is running. Queued messages wait for a busy turn to finish; idle sessions can start immediately. Delivery means accepted by Codex, not acted on.")
    skill += "\nIf CODEX_THREAD_ID is unavailable, prefix commands with AGISTRY_SESSION_ID=<the SessionStart hook's session id>. Do not guess another session's identity.\n"
    backup(skill_dir / "SKILL.md")
    (skill_dir / "SKILL.md").write_text(skill)
    backup(skill_dir / "agistry.sh")
    shutil.copy2(original / "agistry.sh", skill_dir / "agistry.sh")
    (skill_dir / "agistry.sh").chmod(0o755)

    config = home / ".config/agistry/client.env"
    updates = {key: value for key, value in {"AGISTRY_URL": args.url, "AGISTRY_TOKEN": args.token}.items() if value is not None}
    if updates:
        config.parent.mkdir(parents=True, exist_ok=True)
        text = config.read_text() if config.exists() else ""
        backup(config)
        for key, value in updates.items():
            text = re.sub(r"^\s*(?:export\s+)?" + key + r"=.*(?:\n|$)", "", text, flags=re.MULTILINE)
            text = text.rstrip("\n") + "\n" + key + "=" + shlex.quote(value) + "\n"
        config.write_text(text.lstrip("\n"))
        config.chmod(0o600)

    hooks_path = codex_dir / "hooks.json"
    hooks = json.loads(hooks_path.read_text()) if hooks_path.exists() else {}
    hook_command = shlex.join(["bash", str(runtime / "agistry-codex.sh"), "hook"])
    events = hooks.setdefault("hooks", {})
    for event, timeout in (("SessionStart", 10), ("SessionEnd", 3)):
        groups = []
        for group in events.get(event, []):
            other = [handler for handler in group.get("hooks", [])
                     if "agistry-codex.sh" not in handler.get("command", "")]
            if other:
                groups.append({**group, "hooks": other})
        ours = {"hooks": [{"type": "command", "command": hook_command, "timeout": timeout}]}
        if event == "SessionStart":
            ours["matcher"] = "^(startup|resume|clear)$"
        groups.append(ours)
        events[event] = groups
    backup(hooks_path)
    atomic_json(hooks_path, hooks)
    print(f"Installed Codex adapter: {runtime}")
    print(f"Installed Agistry skill: {skill_dir}")
    if not config.exists():
        print(f"Configure AGISTRY_URL and AGISTRY_TOKEN in {config}.")
    print("Open /hooks in Codex to review and trust the new hooks, then start/resume a session.")
    print("Requires sessions hosted by the local shared daemon; --no-daemon and remote endpoints are not supported.")


if __name__ == "__main__":
    main()
