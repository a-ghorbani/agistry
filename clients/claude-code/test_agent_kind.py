"""Exercise shared CLI requests without contacting a registry."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

CLI = Path(__file__).parent / "skills/agistry/agistry.sh"


class AgentKindTest(unittest.TestCase):
    def test_client_kind_and_who_filter(self):
        with tempfile.TemporaryDirectory() as tmp:
            fake_curl = Path(tmp) / "curl"
            fake_curl.write_text('''#!/usr/bin/env python3
import json,sys
args=sys.argv[1:]
body=json.loads(args[args.index('-d')+1]) if '-d' in args else None
url=next(a for a in args if a.startswith(('http://','https://')))
# Never echo authentication headers.
print(json.dumps({'body':body,'url':url}))
''')
            fake_curl.chmod(0o755)
            env = dict(os.environ, PATH=tmp + os.pathsep + os.environ["PATH"], AGISTRY_STATE_DIR=tmp)
            for key in ("AGISTRY_AGENT_KIND", "AGISTRY_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"):
                env.pop(key, None)
            for variable, kind in [("CODEX_THREAD_ID", "codex"), ("CLAUDE_CODE_SESSION_ID", "claude"), ("AGISTRY_SESSION_ID", "")]:
                case_env = dict(env, **{variable: "test-session"})
                for command in ("register", "heartbeat"):
                    result = subprocess.run(["bash", str(CLI), command], env=case_env, text=True, capture_output=True, check=True)
                    body = json.loads(result.stdout)["body"]
                    self.assertEqual(body["session_id"], "test-session")
                    self.assertEqual(body["agent_kind"], kind)
            result = subprocess.run(["bash", str(CLI), "who", "TASK", "reviewer", "--kind", "codex"], env=env, text=True, capture_output=True, check=True)
            self.assertTrue(json.loads(result.stdout)["url"].endswith("/agents?task=TASK&role=reviewer&agent_kind=codex"))
            invalid = subprocess.run(["bash", str(CLI), "who", "--kind", "bad"], env=env, text=True, capture_output=True)
            self.assertNotEqual(invalid.returncode, 0)
            self.assertIn("invalid agent kind", invalid.stderr)


if __name__ == "__main__":
    unittest.main()
