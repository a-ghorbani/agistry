#!/usr/bin/env python3
"""Idempotent installer; preserves other hooks and shared client configuration."""

import argparse
import json
import os
from pathlib import Path
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

    # Config, the agent-neutral skill (~/.agents/skills/agistry) and the device
    # providers are shared with the opencode and pi clients.
    shared = ["bash", str(here.parent / "shared/install.sh")]
    if args.url:
        shared += ["--url", args.url]
    if args.token:
        shared += ["--token", args.token]
    subprocess.run(shared, check=True)
    skill_dir = home / ".agents/skills/agistry"
    config = home / ".config/agistry/client.env"

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
