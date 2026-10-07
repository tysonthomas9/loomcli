#!/usr/bin/env python3
"""Offline regressions for the public evidence oracle; no Loom state is made."""

import importlib.util
import hashlib
import io
import json
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
    def test_public_send_202_and_archive_empty_204(self):
        send = {"message_id": "msg_1", "state": "waiting", "replaced": False}
        response = io.BytesIO(json.dumps(send).encode())
        response.status = 202
        with patch.object(evidence.urllib.request, "urlopen", return_value=response):
            self.assertEqual(evidence.http(evidence.ROOT + "/agents/agt_1/messages", "POST",
                                           {"text": "hello"}, "req-1"), send)
        response = io.BytesIO(b"")
        response.status = 204
        with patch.object(evidence.urllib.request, "urlopen", return_value=response):
            self.assertIsNone(evidence.http(evidence.ROOT + "/agents/agt_1/archive", "POST",
                                            {"reason": "cancelled"}, "req-2"))

    def test_ui_receipts_require_send_202_and_withdraw_200(self):
        send = {"method": "POST", "path": evidence.ROOT + "/agents/agt_1/messages",
                "key": "send-1", "status": 202, "result": {"message_id": "msg_1", "state": "waiting"}}
        withdraw = {"method": "DELETE", "path": evidence.ROOT + "/agents/agt_1/messages/waiting",
                    "key": "clear-1", "status": 200, "result": {"result": "withdrawn"}}
        with patch.object(evidence, "agent_id", return_value="agt_1"):
            self.assertTrue(evidence.valid_ui_receipt(send, "waiting"))
            self.assertTrue(evidence.valid_ui_receipt(withdraw, "waiting"))
            self.assertFalse(evidence.valid_ui_receipt({**send, "status": 201}, "waiting"))
            self.assertFalse(evidence.valid_ui_receipt({**withdraw, "status": 204, "result": None}, "waiting"))

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
                    {"method": "POST", "path": evidence.ROOT + "/agents/agt_1/messages", "key": "old", "body": '{"text":"A"}', "result": {"message_id": "m1", "state": "waiting", "replaced": False}},
                    {"method": "DELETE", "path": evidence.ROOT + "/agents/agt_1/messages/waiting", "key": "clear", "body": "", "result": {"result": "withdrawn"}},
                ]}
                evidence.save("waiting", "cleared", before)
                def fake_http(path, method="GET", body=None, key=None):
                    self.assertEqual(method, "POST")
                    self.assertEqual(key, "old")
                    return {"message_id": "m1", "state": "waiting", "replaced": False}
                with patch.object(evidence, "http", side_effect=fake_http) as request, \
                     patch.object(evidence, "agent", return_value={"waiting_messages": []}), \
                     patch.object(evidence, "pages", return_value=before["events"]):
                    evidence.replay("waiting", "cleared", "cleared", "1")
                request.assert_called_once()

    def test_replay_mismatch_saves_only_public_receipt_fields(self):
        original = {"message_id": "msg_original", "state": "waiting", "replaced": False,
                    "turn_id": "turn_1", "interrupted": False, "secret": "must-not-save"}
        cases = {
            "wrong-state": {**original, "state": "handed"},
            "wrong-id": {**original, "message_id": "msg_other"},
            "missing-field": {k: v for k, v in original.items() if k != "replaced"},
            "null": None,
        }
        for name, retry in cases.items():
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                with patch.object(evidence, "OUT", Path(directory)), patch.object(evidence, "agent_id", return_value="agt_1"), \
                     patch.object(evidence, "http", return_value=retry):
                    evidence.save("waiting", "settled", {"waiting": [], "events": []})
                    evidence.save("waiting", "final-waiting", {"ui_calls": [{
                        "method": "POST", "path": evidence.ROOT + "/agents/agt_1/messages",
                        "key": "request-1", "body": '{"text":"must-not-save"}', "result": original}]})
                    with self.assertRaisesRegex(AssertionError, "retry changed stable public receipt fields"):
                        evidence.replay("waiting", "final-waiting", "settled", "1")
                    diagnostic = Path(directory) / "waiting-settled-receipt-mismatch.json"
                    saved = json.loads(diagnostic.read_text())
                    fields = ("message_id", "state", "replaced", "turn_id", "interrupted")
                    self.assertEqual(saved, {"request_id": "request-1",
                                             "original": {k: original[k] for k in fields if k in original},
                                             "retry": {k: retry[k] for k in fields if isinstance(retry, dict) and k in retry}})
                    self.assertNotIn("must-not-save", diagnostic.read_text())

    def test_handed_replay_needs_matching_saved_delivery_and_native_proof(self):
        original = {"message_id": "msg_owned", "state": "waiting", "replaced": False}
        key = "msg_" + hashlib.sha256(b"agt_1\x00request-1").hexdigest()[:26]
        delivered = {"seq": 1, "event_id": "evt_delivered", "kind": "message.delivered",
                     "payload": {"text": "PRIVATE_TEXT", "inputKey": key}}
        call = {"method": "POST", "path": evidence.ROOT + "/agents/agt_1/messages",
                "key": "request-1", "body": '{"text":"PRIVATE_TEXT"}', "result": original}
        for case in ("accepted", "no-delivery", "wrong-key", "wrong-id", "wrong-flag", "extra-field", "extra-event", "native-repeat"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as directory:
                before = {"waiting": [], "events": [delivered], "ui_calls": [call]}
                replay = {**original, "state": "handed"}
                if case == "wrong-id":
                    replay["message_id"] = "msg_foreign"
                elif case == "wrong-flag":
                    replay["replaced"] = True
                elif case == "extra-field":
                    replay["secret"] = "PRIVATE_SECRET"
                events = [] if case == "no-delivery" else [{**delivered, "payload": {**delivered["payload"]}}]
                if case == "wrong-key":
                    events[0]["payload"]["inputKey"] = "msg_foreign"
                after = events + ([{"seq": 2, "event_id": "evt_extra", "kind": "message.delivered"}] if case == "extra-event" else [])
                with patch.object(evidence, "OUT", Path(directory)), patch.object(evidence, "agent_id", return_value="agt_1"), \
                     patch.object(evidence, "http", return_value=replay), \
                     patch.object(evidence, "agent", return_value={"waiting_messages": []}), \
                     patch.object(evidence, "pages", return_value=after):
                    evidence.save("waiting", "settled", {**before, "events": events})
                    evidence.save("waiting", "final-waiting", {"ui_calls": [call]})
                    evidence.save("waiting", "settled-delivery", {"event_id": "evt_delivered", "native_input_key": key, "text": "PRIVATE_TEXT"})
                    evidence.save("waiting", "settled-native-input", {"agent_id": "agt_1", "input_key": key, "native_user_message_count": 1})

                    def probe(_label, stage):
                        self.assertEqual(stage, "settled")
                        if case == "native-repeat":
                            evidence.save("waiting", "settled-native-input", {"agent_id": "agt_1", "input_key": key,
                                                                             "native_user_message_count": 2})

                    with patch.object(evidence, "native_count", side_effect=probe) as native:
                        if case == "accepted":
                            evidence.replay("waiting", "final-waiting", "settled", "1")
                            native.assert_called_once()
                            saved_replay = evidence.load("waiting", "settled-replay")
                            self.assertEqual(saved_replay["receipts"], [original])
                            self.assertEqual(saved_replay["replayed_receipts"], [replay])
                        else:
                            with self.assertRaises(AssertionError):
                                evidence.replay("waiting", "final-waiting", "settled", "1")
                    if case not in ("accepted", "extra-event", "native-repeat"):
                        diagnostic = (Path(directory) / "waiting-settled-receipt-mismatch.json").read_text()
                        self.assertNotIn("PRIVATE_TEXT", diagnostic)
                        self.assertNotIn("PRIVATE_SECRET", diagnostic)

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
