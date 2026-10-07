import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest
from unittest.mock import patch
import urllib.error

from agistry_codex import Bridge, CodexProxy, atomic_json, read_json


SID = "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"
OTHER = "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"
HERE = Path(__file__).resolve().parent

# Independent fixture for the daemon's raw WebSocket control socket over proxy.
PROXY_FIXTURE = r'''#!/usr/bin/env python3
import base64,hashlib,json,os,struct,sys
if os.environ.get("FIXTURE_INVOCATIONS"):
    with open(os.environ["FIXTURE_INVOCATIONS"],"a") as log: log.write("proxy\n")
reader,writer=sys.stdin.buffer,sys.stdout.buffer
headers={}
assert reader.readline().startswith(b"GET / HTTP/1.1")
while True:
    line=reader.readline()
    if line==b"\r\n": break
    key,value=line.decode().split(":",1);headers[key.lower()]=value.strip()
accept=base64.b64encode(hashlib.sha1((headers["sec-websocket-key"]+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()).decode()
writer.write(("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+accept+"\r\n\r\n").encode());writer.flush()
def exact(n):
    out=b""
    while len(out)<n:
        part=reader.read(n-len(out))
        if not part: raise SystemExit(0)
        out+=part
    return out
def send(payload,opcode=1,final=True):
    length=len(payload)
    header=bytes([(0x80 if final else 0)|opcode])
    header+=bytes([length]) if length<126 else bytes([126])+struct.pack("!H",length)
    writer.write(header+payload);writer.flush()
while True:
    opcode,length=exact(2)
    assert length&0x80, "Client frames must be masked"
    length&=127
    if length==126: length=struct.unpack("!H",exact(2))[0]
    elif length==127: length=struct.unpack("!Q",exact(8))[0]
    mask=exact(4);data=exact(length)
    data=bytes(v^mask[i%4] for i,v in enumerate(data))
    if opcode&15==10: continue
    req=json.loads(data)
    if "id" not in req: continue
    if req["method"]=="initialize": result={}
    elif os.environ.get("FIXTURE_INVOCATIONS"): result={"data":[]}
    else: result={"data":["second"]} if req["params"].get("cursor") else {"data":["first"],"nextCursor":"next"}
    send(b"ping",opcode=9)
    send(json.dumps({"method":"thread/status/changed","params":{}}).encode())
    payload=json.dumps({"id":req["id"],"result":result}).encode()
    send(payload[:5],final=False);send(payload[5:],opcode=0)
'''


class FakeRegistry:
    def __init__(self):
        self.calls = []
        self.pending = [{"msg_id": "m1", "from": "TASK:reviewer", "body": "look at tests"}]
        self.fail_ack = False
        self.conflict = False
        self.agents = {}

    def call(self, path, body=None, **kwargs):
        self.calls.append((path, body))
        if path == "/assign" and self.conflict:
            raise urllib.error.HTTPError("fixture", 409, "conflict", {}, None)
        if path in ("/register", "/assign", "/heartbeat", "/deregister"):
            sid = body["session_id"]
            agent = self.agents.setdefault(sid, {"state": "unassigned"})
            if path == "/register":
                # Real /register preserves existing state, even 'gone'.
                agent.update({k: v for k, v in body.items() if k in ("cwd", "host")})
            elif path == "/assign":
                agent.update(body, state="active")
            elif path == "/heartbeat":
                agent["state"] = "active" if agent.get("role") else "unassigned"
            else:
                agent["state"] = "gone"
        if path.startswith("/inbox?"):
            return {"messages": self.pending.copy()}
        if path == "/ack":
            if self.fail_ack:
                raise OSError("ack unavailable")
            self.pending = [m for m in self.pending if m["msg_id"] not in body["msg_ids"]]
        return {}


class BridgeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.registry = FakeRegistry()
        self.bridge = Bridge(self.root / "state", registry=self.registry, codex="fixture-codex")
        self.bridge.open_journal()
        self.addCleanup(lambda: self.bridge.journal.close())
        atomic_json(self.bridge.sessions / (SID + ".json"), {"active": True})
        atomic_json(self.bridge.state_dir / (SID + ".json"), {
            "session_id": SID, "task": "TASK", "role": "implementer", "cwd": "/work", "host": "box",
        })

    @patch("agistry_codex.subprocess.run")
    def test_success_queues_exact_session_then_acks(self, run):
        run.return_value.returncode = 0
        self.bridge.tick({SID})
        args = run.call_args.args[0]
        self.assertEqual(args[:4], ["fixture-codex", "queue", "--thread", SID])
        payload = json.loads(args[5].split("\n", 1)[1])
        self.assertEqual(payload, {"msg_id": "m1", "from": "TASK:reviewer", "body": "look at tests"})
        self.assertEqual(self.registry.pending, [])
        self.assertTrue(any(path == "/assign" and body["role"] == "implementer" for path, body in self.registry.calls))

    @patch("agistry_codex.subprocess.run")
    def test_failed_queue_leaves_mail_and_no_receipt(self, run):
        run.return_value.returncode = 1
        self.bridge.tick({SID})
        self.assertEqual(len(self.registry.pending), 1)
        self.assertFalse(any(path == "/ack" for path, _ in self.registry.calls))
        self.assertEqual(self.bridge.journal.execute("SELECT count(*) FROM receipts").fetchone()[0], 0)

    @patch("agistry_codex.subprocess.run")
    def test_ack_failure_and_restart_do_not_requeue(self, run):
        run.return_value.returncode = 0
        self.registry.fail_ack = True
        self.bridge.tick({SID})
        restarted = Bridge(self.root / "state", registry=self.registry, codex="fixture-codex")
        restarted.open_journal()
        try:
            self.registry.fail_ack = False
            restarted.tick({SID})
        finally:
            restarted.journal.close()
        self.assertEqual(run.call_count, 1)
        self.assertEqual(self.registry.pending, [])

    @patch("agistry_codex.subprocess.run")
    def test_accepted_mail_is_acked_even_after_recipient_closes(self, run):
        run.return_value.returncode = 0
        self.registry.fail_ack = True
        self.bridge.tick({SID})
        self.registry.fail_ack = False
        self.bridge.tick(set())
        self.assertEqual(self.registry.pending, [])
        self.assertEqual(run.call_count, 1)

    @patch("agistry_codex.subprocess.run")
    def test_dead_threads_are_not_polled_or_revived(self, run):
        self.bridge.tick({OTHER})
        run.assert_not_called()
        self.assertEqual(self.registry.calls, [("/deregister", {"session_id": SID})])
        self.registry.calls.clear()
        self.bridge.tick({OTHER})
        self.assertEqual(self.registry.calls, [])

    @patch("agistry_codex.subprocess.run")
    def test_closed_thread_not_revived_even_if_daemon_still_loaded(self, run):
        atomic_json(self.bridge.sessions / (SID + ".json"), {"active": False})
        self.bridge.tick({SID})
        run.assert_not_called()
        self.assertEqual(self.registry.calls, [("/deregister", {"session_id": SID})])

    @patch("agistry_codex.subprocess.run")
    def test_identity_conflict_is_surfaced_and_mail_kept(self, run):
        self.registry.conflict = True
        self.bridge.tick({SID})
        run.assert_not_called()
        self.assertTrue((self.bridge.state_dir / (SID + ".conflict")).exists())
        self.assertEqual(len(self.registry.pending), 1)
        self.registry.conflict = False
        run.return_value.returncode = 0
        self.bridge.tick({SID})
        self.assertFalse((self.bridge.state_dir / (SID + ".conflict")).exists())
        self.assertEqual(len(self.registry.pending), 0)

    @patch("agistry_codex.subprocess.run")
    def test_reconcile_recovers_after_registry_reset(self, run):
        run.return_value.returncode = 0
        self.bridge.tick({SID})
        self.registry.calls.clear()
        self.bridge.last_reconcile.clear()  # Also cleared on daemon reconnect.
        self.bridge.tick({SID})
        assign = [body for path, body in self.registry.calls if path == "/assign"]
        self.assertEqual(assign[0]["task"], "TASK")
        self.assertEqual(assign[0]["role"], "implementer")
        self.assertEqual(assign[0]["agent_kind"], "codex")

    @patch("agistry_codex.subprocess.Popen")
    def test_resume_preserves_identity_and_compaction_does_not_spawn(self, popen):
        event = {"hook_event_name": "SessionStart", "source": "resume", "session_id": SID, "cwd": "/new"}
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.bridge.hook(event)
        self.assertEqual(read_json(self.bridge.state_dir / (SID + ".json"))["role"], "implementer")
        self.assertEqual(read_json(self.bridge.state_dir / (SID + ".json"))["cwd"], "/new")
        self.assertIn(SID, json.loads(out.getvalue())["hookSpecificOutput"]["additionalContext"])
        self.assertEqual(read_json(self.bridge.state_dir / (SID + ".json"))["agent_kind"], "codex")
        self.assertEqual(popen.call_count, 1)
        event["source"] = "compact"
        self.bridge.hook(event)
        self.assertEqual(popen.call_count, 1)

    @patch("agistry_codex.subprocess.Popen")
    def test_subagents_and_invalid_ids_are_ignored(self, popen):
        for fields in ({"agent_type": "reviewer"}, {"agent_id": "child"}, {"session_id": "../../outside"}):
            self.bridge.hook({"hook_event_name": "SessionStart", "session_id": SID, **fields})
        popen.assert_not_called()

    def test_session_end_records_closure_and_deregisters_without_context_output(self):
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.bridge.hook({"hook_event_name": "SessionEnd", "session_id": SID})
        self.assertEqual(out.getvalue(), "")
        self.assertFalse(read_json(self.bridge.sessions / (SID + ".json"))["active"])
        self.assertEqual(self.registry.calls, [("/deregister", {"session_id": SID})])

    @patch("agistry_codex.subprocess.Popen")
    @patch("agistry_codex.subprocess.run")
    def test_unassigned_thread_is_live_again_after_unload_and_resume(self, run, popen):
        run.return_value.returncode = 0
        self.registry.pending = []
        atomic_json(self.bridge.state_dir / (SID + ".json"), {"session_id": SID, "cwd": "/old", "host": "box"})
        self.bridge.tick({SID})
        self.assertEqual(self.registry.agents[SID]["state"], "unassigned")
        self.bridge.tick(set())
        self.assertEqual(self.registry.agents[SID]["state"], "gone")
        with contextlib.redirect_stdout(io.StringIO()):
            self.bridge.hook({"hook_event_name": "SessionStart", "source": "resume", "session_id": SID, "cwd": "/new"})
        self.bridge.tick({SID})
        self.assertEqual(self.registry.agents[SID]["state"], "unassigned")
        self.assertEqual(self.registry.agents[SID]["cwd"], "/new")
        self.assertNotIn("role", self.registry.agents[SID])


class ProxyTests(unittest.TestCase):
    def test_proxy_protocol_pages_and_handles_notifications(self):
        with tempfile.TemporaryDirectory() as tmp:
            codex = Path(tmp) / "fixture-codex"
            codex.write_text(PROXY_FIXTURE)
            codex.chmod(0o755)
            proxy = CodexProxy(str(codex))
            try:
                self.assertEqual(proxy.loaded(), {"first", "second"})
            finally:
                proxy.close()

    def test_bridge_singleton_and_graceful_shutdown(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            fixture = root / "codex"
            invocations = root / "invocations"
            fixture.write_text(PROXY_FIXTURE)
            fixture.chmod(0o755)
            env = {**os.environ, "CODEX_HOME": str(root / "home"), "AGISTRY_STATE_DIR": str(root / "state"),
                   "AGISTRY_CODEX_BIN": str(fixture), "FIXTURE_INVOCATIONS": str(invocations), "AGISTRY_CODEX_POLL_SEC": "0.02"}
            first = subprocess.Popen(["bash", str(HERE / "agistry-codex.sh"), "bridge"], env=env,
                                     stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            try:
                deadline = time.monotonic() + 5
                while not invocations.exists() and time.monotonic() < deadline:
                    time.sleep(.02)
                self.assertTrue(invocations.exists())
                second = subprocess.run(["bash", str(HERE / "agistry-codex.sh"), "bridge"], env=env,
                                        capture_output=True, timeout=5)
                self.assertEqual(second.returncode, 0, second.stderr)
                self.assertEqual(invocations.read_text().splitlines(), ["proxy"])
                first.terminate()
                self.assertEqual(first.wait(timeout=5), 0)
            finally:
                if first.poll() is None:
                    first.kill()
                    first.wait()
                first.stderr.close()


class InstallTests(unittest.TestCase):
    def test_install_is_idempotent_preserves_handlers_and_config_and_cli_ids(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            codex_home = home / "codex home"
            codex_home.mkdir()
            hooks = {"description": "keep", "hooks": {"SessionStart": [{"hooks": [
                {"type": "command", "command": "keep-me"},
                {"type": "command", "command": "bash /old/agistry-codex.sh hook"},
            ]}]}}
            atomic_json(codex_home / "hooks.json", hooks)
            config = home / ".config/agistry/client.env"
            config.parent.mkdir(parents=True)
            config.write_text("AGISTRY_URL=http://old\nAGISTRY_TOKEN=keep-token\nAGISTRY_PROVIDERS=adb\n")
            fixture = home / "codex"
            fixture.write_text("#!/bin/sh\nexit 0\n")
            fixture.chmod(0o755)
            env = {**os.environ, "HOME": tmp, "CODEX_HOME": str(codex_home), "AGISTRY_CODEX_BIN": str(fixture)}
            for _ in range(2):
                subprocess.run(["bash", str(HERE / "install.sh"), "--url", "http://new"], env=env, check=True, capture_output=True)
            after = read_json(codex_home / "hooks.json")
            commands = [h["command"] for g in after["hooks"]["SessionStart"] for h in g["hooks"]]
            self.assertEqual(after["description"], "keep")
            self.assertEqual(len(commands), 2)
            self.assertIn("keep-me", commands)
            self.assertIn("AGISTRY_TOKEN=keep-token", config.read_text())
            self.assertIn("AGISTRY_PROVIDERS=adb", config.read_text())
            skill = home / ".agents/skills/agistry/SKILL.md"
            self.assertTrue(skill.exists())
            self.assertNotIn("~/.claude/", skill.read_text())
            self.assertNotIn("$CLAUDE_CODE_SESSION_ID", skill.read_text())
            # Verify identity precedence through the actual shared shell CLI.
            curl = home / "curl"
            curl.write_text('#!/usr/bin/env python3\nimport sys\nprint(sys.argv[-1])\n')
            curl.chmod(0o755)
            env.update(PATH=tmp + os.pathsep + env["PATH"], CLAUDE_CODE_SESSION_ID="claude", CODEX_THREAD_ID="codex-thread")
            cli = home / ".agents/skills/agistry/agistry.sh"
            result = subprocess.check_output(["bash", str(cli), "heartbeat"], env=env, text=True)
            self.assertEqual(json.loads(result)["session_id"], "codex-thread")
            env["AGISTRY_SESSION_ID"] = "explicit"
            result = subprocess.check_output(["bash", str(cli), "heartbeat"], env=env, text=True)
            self.assertEqual(json.loads(result)["session_id"], "explicit")


if __name__ == "__main__":
    unittest.main()
