#!/usr/bin/env python3
"""Offline boundary oracles for the live receipts edge observer."""

import importlib.util
import hashlib
import io
import json
import os
from pathlib import Path
import re
import tempfile
import unittest
from unittest import mock
import urllib.error

SCRIPT = Path(__file__).with_name("coverage-receipts-stream-edges.py")
ENV = {"AFT_API_URL": "http://127.0.0.1:1", "AFT_BASE_URL": "http://127.0.0.1:2",
       "AFT_WS": "LOCALMODE", "RUN_ID": "offline", "AFT_AGENT_FLOW_REPO": "/workspace/source-repo",
       "AFT_REAL_MODEL": "provider/model", "AFT_WORK_DIR": "/tmp/edges-offline"}


def module():
    with mock.patch.dict(os.environ, ENV):
        spec = importlib.util.spec_from_file_location("edges", SCRIPT)
        value = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(value)
        return value


class Response:
    def __init__(self, status, body):
        self.status = status
        self.body = io.BytesIO(json.dumps(body).encode() if body is not None else b"")

    def __enter__(self):
        return self

    def __exit__(self, *_):
        pass

    def read(self):
        return self.body.read()


class EdgeOracles(unittest.TestCase):
    def test_unicode_escape_and_json_cap_are_exact_bytes(self):
        edge = module()
        hundred = edge.text_for("hundred-k")
        near = edge.text_for("near-cap")
        self.assertGreater(len(hundred.encode("utf-8")), 100_000)
        self.assertEqual(len(edge.packed({"text": near})), edge.CAP - 1)
        self.assertGreater(len(edge.packed({"text": near + "xx"})), edge.CAP)
        self.assertEqual(json.loads(edge.packed({"text": near}))["text"].encode(), near.encode())
        self.assertIn(b"\\n", edge.packed({"text": hundred}))
        self.assertIn("日本語".encode(), edge.packed({"text": hundred}))

    def test_public_send_202_and_rejected_body_413_keep_distinct_statuses(self):
        edge = module()
        receipt = {"message_id": "m1", "state": "waiting", "replaced": False}
        with mock.patch.object(edge.urllib.request, "urlopen", return_value=Response(202, receipt)):
            self.assertEqual(edge.call("/messages", "POST", {"text": "hi"}, "req-1", 202), receipt)
        rejected = urllib.error.HTTPError("/messages", 413, "too large", {}, io.BytesIO(b'{"error":"request body too large"}'))
        with mock.patch.object(edge.urllib.request, "urlopen", side_effect=rejected):
            self.assertEqual(edge.call("/messages", "POST", {"text": "x"}, "req-2", 413),
                             {"error": "request body too large"})
        with mock.patch.object(edge.urllib.request, "urlopen", return_value=Response(204, None)):
            self.assertIsNone(edge.call("/archive", "POST", {}, "req-3", 204))

    def test_over_cap_does_not_accept_receipt_or_change_slot_and_events(self):
        edge = module()
        near = edge.text_for("near-cap")
        slot = {"waiting_messages": [{"text": near}]}
        observed = []

        def fake_call(_path, method="GET", body=None, key=None, expected=200):
            observed.append((method, key, expected, len(edge.packed(body)) if body else 0))
            self.assertEqual(expected, 413)
            return {"error": "request body too large"}

        with tempfile.TemporaryDirectory() as temporary:
            edge.OUT = Path(temporary)
            with mock.patch.object(edge, "agent", return_value=slot), \
                 mock.patch.object(edge, "event_ids", return_value=["evt_1"]), \
                 mock.patch.object(edge, "agent_path", return_value="/agent"), \
                 mock.patch.object(edge, "call", side_effect=fake_call):
                edge.large_over()
            self.assertEqual(len(observed), 1)
            self.assertEqual(observed[0][2], 413)
            self.assertGreater(observed[0][3], edge.CAP)
            saved = json.loads((edge.OUT / "large-over-cap.json").read_text())
            self.assertEqual(saved["event_ids"], ["evt_1"])

    def test_native_input_key_matches_source_contract(self):
        edge = module()
        with mock.patch.object(edge, "agent_id", return_value="agt_owned"):
            expected = "msg_" + hashlib.sha256(b"agt_owned\x00request-1").hexdigest()[:26]
            self.assertEqual(edge.native_key("large", "request-1"), expected)

    def test_owned_native_helper_uses_direct_podman_exec_without_compose_flag(self):
        shell = SCRIPT.with_name("coverage-receipts-stream-edges-native.sh").read_text()

        def supported(source):
            found = re.search(r'^result="\$\(agent_flows_podman (exec[^\n]*?) node -e ', source, re.M)
            return found is not None and found[1] == 'exec "$container"'

        self.assertTrue(supported(shell))
        self.assertFalse(supported(shell.replace('exec "$container"', 'exec -T "$container"')))
        self.assertRegex(shell, r'agent_flows_check_manifest[\s\S]*agent_flows_check_container "\$container"[\s\S]*agent_flows_podman exec "\$container" node -e')

    def test_repo_rejections_use_owned_read_only_fixtures_and_leave_inventory(self):
        edge = module()
        inventory = {"worktrees": ["agt_other"], "agents": 1, "sessions": 1,
                     "non_git_dir": "/root/.loom/workspaces/LOCALMODE",
                     "unknown_repo": "/root/.loom/workspaces/LOCALMODE/no-such-repo-offline"}
        bodies = []

        def refuse(_path, method="GET", body=None, key=None, expected=200):
            self.assertEqual((method, expected), ("POST", 400))
            bodies.append(body)
            return {"code": "preset_invalid", "error": f'repo "{body["repo"]}" is not the absolute path of a repo clone'}

        with tempfile.TemporaryDirectory() as temporary:
            edge.OUT = Path(temporary)
            edge.save("create", "baseline", {"inventory": inventory})
            with mock.patch.object(edge, "call", side_effect=refuse), \
                 mock.patch.object(edge, "listed_create", return_value=[]), \
                 mock.patch.object(edge, "native", return_value=inventory):
                edge.create_refuse("repo-missing")
                edge.create_refuse("repo-nongit")
        self.assertEqual([body["repo"] for body in bodies],
                         [inventory["unknown_repo"], inventory["non_git_dir"]])
        self.assertTrue(all(body["name"] == edge.name("create") and body["base_ref"] == "main" for body in bodies))


if __name__ == "__main__":
    unittest.main()
