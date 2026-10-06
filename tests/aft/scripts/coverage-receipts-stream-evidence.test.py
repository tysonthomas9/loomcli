#!/usr/bin/env python3
"""Offline regressions for the public evidence oracle; no Loom state is made."""

import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

os.environ.setdefault("AFT_API_URL", "http://127.0.0.1:1")
os.environ.setdefault("AFT_WS", "LOCALMODE")
os.environ.setdefault("RUN_ID", "validation")
os.environ.setdefault("AFT_WORK_DIR", "/private/tmp/aft-receipts-validation")

spec = importlib.util.spec_from_file_location(
    "coverage_rs", Path(__file__).with_name("coverage-receipts-stream-evidence.py"))
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)


def row(seq, kind="message.waiting"):
    return {"seq": seq, "event_id": f"agt_1:{seq}", "agent_id": "agt_1", "kind": kind}


class OracleTests(unittest.TestCase):
    def test_paging_pins_first_snapshot_when_new_rows_arrive(self):
        calls = []

        def fake_http(path):
            calls.append(path)
            if "snapshot=" not in path:
                return {"events": [row(1), row(2)], "snapshot_seq": 3, "next": 2, "more": True}
            self.assertIn("snapshot=3", path)
            return {"events": [row(3)], "snapshot_seq": 3, "next": 3, "more": False}

        with patch.object(evidence, "agent_id", return_value="agt_1"), patch.object(evidence, "http", side_effect=fake_http):
            self.assertEqual([e["seq"] for e in evidence.pages("waiting")], [1, 2, 3])
        self.assertEqual(len(calls), 2)

    def test_stale_send_replay_never_replays_withdraw(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(evidence, "OUT", Path(directory)), patch.object(evidence, "agent_id", return_value="agt_1"):
                before = {"waiting": [], "events": [row(1), row(2, "message.withdrawn")], "ui_calls": [
                    {"method": "POST", "path": evidence.ROOT + "/agents/agt_1/messages", "key": "old", "body": '{"text":"A"}', "result": {"message_id": "m1"}},
                    {"method": "DELETE", "path": evidence.ROOT + "/agents/agt_1/messages/waiting", "key": "clear", "body": "", "result": {"result": "withdrawn"}},
                ]}
                evidence.save("waiting", "cleared", before)
                def fake_http(path, method="GET", body=None, key=None):
                    self.assertEqual(method, "POST")
                    self.assertEqual(key, "old")
                    return {"message_id": "m1"}
                with patch.object(evidence, "http", side_effect=fake_http) as request, \
                     patch.object(evidence, "agent", return_value={"waiting_messages": []}), \
                     patch.object(evidence, "pages", return_value=before["events"]):
                    evidence.replay("waiting", "cleared", "cleared", "1")
                request.assert_called_once()

    def test_create_replay_rejects_new_saved_event_after_restart(self):
        agent = {"agent_id": "agt_1", "name": "coverage-rs-create-validation", "preset": "lead",
                 "repo": "/repo", "base_ref": "main", "external_key": "coverage-rs-create-validation",
                 "created_by_kind": "user", "parent_agent_id": None, "harness": "opencode",
                 "model": "openai/real", "worktree_path": "/owned/tree", "state": "idle"}
        request = {"body": {"name": agent["name"], "repo": agent["repo"], "base_ref": "main",
                            "external_key": agent["external_key"], "overrides": {"model": agent["model"]}},
                   "request_id": "create-one", "worktree_path": agent["worktree_path"]}
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(evidence, "OUT", Path(directory)), patch.object(evidence, "agent_id", return_value="agt_1"), \
                 patch.object(evidence, "agent", return_value=agent), \
                 patch.object(evidence, "pages", side_effect=[[row(1, "agent.created")],
                                                             [row(1, "agent.created"), row(2, "agent.created")]]), \
                 patch.object(evidence, "http", return_value=agent):
                evidence.save("create", "request", request)
                with self.assertRaisesRegex(AssertionError, "appended an event"):
                    evidence.create_replay("after-restart")


if __name__ == "__main__":
    unittest.main()
