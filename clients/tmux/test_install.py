"""Installer regression tests; writes only to an isolated temporary home."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class InstallTests(unittest.TestCase):
    def test_mixed_groups_preserve_unrelated_handlers_on_install_and_uninstall(self):
        installer = Path(__file__).resolve().with_name("install.sh")
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            files = [home / ".claude/settings.json", home / ".codex/hooks.json"]
            unrelated = {"type": "command", "command": "/custom/record-session", "timeout": 7}
            for file in files:
                file.parent.mkdir()
                file.write_text(json.dumps({"description": "preserve", "hooks": {
                    "SessionStart": [{"matcher": "startup|resume", "hooks": [
                        {"type": "command", "command": "/old/bin/agent-state codex"}, unrelated,
                    ]}],
                    "CustomEvent": [{"hooks": [{"type": "command", "command": "custom-only"}]}],
                }}))
            env = {**os.environ, "HOME": tmp, "BIN_DIR": str(home / "bin")}
            for args in ([], [], ["--uninstall"]):
                subprocess.run(["bash", str(installer), *args], env=env, check=True, capture_output=True)
                for file in files:
                    hooks = json.loads(file.read_text())
                    self.assertEqual(hooks["description"], "preserve")
                    self.assertEqual(hooks["hooks"]["CustomEvent"][0]["hooks"][0]["command"], "custom-only")
                    start = hooks["hooks"]["SessionStart"]
                    retained = [group for group in start if unrelated in group["hooks"]]
                    self.assertEqual(len(retained), 1)
                    self.assertEqual(retained[0]["matcher"], "startup|resume")
                    self.assertEqual(retained[0]["hooks"], [unrelated])
                    state_handlers = [h for group in start for h in group["hooks"] if "agent-state" in h["command"]]
                    self.assertEqual(len(state_handlers), 0 if args else 1)


if __name__ == "__main__":
    unittest.main()
