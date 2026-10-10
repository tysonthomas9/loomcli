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
    @staticmethod
    def approval_fixture(edge):
        a = {"agent_id": "agt_owned", "preset": "pr-review-interactive",
             "state": "waiting", "waiting_on": "approval", "running_turn_id": "turn_owned",
             "open_asks": [{"id": "ask_owned", "type": "approval", "about": "PRIVATE_COMMAND"}],
             "waiting_messages": []}
        evs = [{"kind": "ask.opened", "event_id": "evt_ask", "turn_id": "turn_owned",
                "payload": {"askId": "ask_owned", "text": "PRIVATE_COMMAND"}}]
        return a, evs

    def test_parsed_interrupt_case_selects_exact_lead_model_before_claim(self):
        import yaml

        suite = yaml.safe_load((SCRIPT.parents[1] / "live-agent-coverage-suites/receipts-stream-edges.test.yaml").read_text())
        self.assertEqual(suite["suite"], "live-receipts-stream-edges")
        self.assertEqual(len(suite["tests"]), 3)
        run = next(step["run"] for step in suite["tests"][0]["steps"] if "claim-interrupt" in step.get("run", ""))
        self.assertLess(run.index('"$AFT_SELECT_AGENT_MODEL" "coverage-rs-edges-interrupt-${RUN_ID}"'),
                        run.index("claim-interrupt"))

    def test_real_waiting_approval_baseline_is_strict_and_saves_bounded_evidence(self):
        for invalid in ("active", "wrong-waiting-on", "missing-ask", "wrong-type", "stale-turn",
                        "wrong-ask-id", "duplicate-ask", "missing-event"):
            with self.subTest(invalid=invalid), tempfile.TemporaryDirectory() as directory:
                edge = module()
                edge.OUT = Path(directory)
                a, evs = self.approval_fixture(edge)
                if invalid == "active":
                    a["state"] = "active"
                elif invalid == "wrong-waiting-on":
                    a["waiting_on"] = "input"
                elif invalid == "missing-ask":
                    a["open_asks"] = []
                elif invalid == "wrong-type":
                    a["open_asks"][0]["type"] = "question"
                elif invalid == "stale-turn":
                    evs[0]["turn_id"] = "turn_stale"
                elif invalid == "wrong-ask-id":
                    evs[0]["payload"]["askId"] = "ask_foreign"
                elif invalid == "duplicate-ask":
                    evs.append({**evs[0], "event_id": "evt_duplicate"})
                else:
                    evs.clear()
                with mock.patch.object(edge, "agent", return_value=a), mock.patch.object(edge, "events", return_value=evs):
                    with self.assertRaises(AssertionError):
                        edge.ask_baseline()
                saved = edge.load("large", "ask-precondition")
                self.assertEqual(saved["agent_id"], "agt_owned")
                self.assertEqual(saved["state"], a["state"])
                self.assertEqual(saved["waiting_on"], a["waiting_on"])
                self.assertEqual(saved["event_ids"], [e["event_id"] for e in evs])
                self.assertNotIn("PRIVATE_COMMAND", (edge.OUT / "large-ask-precondition.json").read_text())
                self.assertFalse((edge.OUT / "large-ask.json").exists())

        with tempfile.TemporaryDirectory() as directory:
            edge = module()
            edge.OUT = Path(directory)
            a, evs = self.approval_fixture(edge)
            with mock.patch.object(edge, "agent", return_value=a), mock.patch.object(edge, "events", return_value=evs):
                edge.ask_baseline()
            self.assertEqual(edge.load("large", "ask")["ask_id"], "ask_owned")
            self.assertEqual(edge.load("large", "ask")["turn_id"], "turn_owned")

    def test_large_send_rechecks_same_waiting_approval_and_retains_failure_snapshot(self):
        for invalid in (None, "active", "wrong-waiting-on", "changed-turn", "changed-ask", "duplicate-ask"):
            with self.subTest(invalid=invalid), tempfile.TemporaryDirectory() as directory:
                edge = module()
                edge.OUT = Path(directory)
                edge.save("large", "ask", {"turn_id": "turn_owned", "ask_id": "ask_owned"})
                a, evs = self.approval_fixture(edge)
                a["waiting_messages"] = [{"text": edge.text_for("hundred-k")}]
                if invalid == "active":
                    a["state"] = "active"
                elif invalid == "wrong-waiting-on":
                    a["waiting_on"] = "input"
                elif invalid == "changed-turn":
                    a["running_turn_id"] = evs[0]["turn_id"] = "turn_other"
                elif invalid == "changed-ask":
                    a["open_asks"][0]["id"] = evs[0]["payload"]["askId"] = "ask_other"
                elif invalid == "duplicate-ask":
                    evs.append({**evs[0], "event_id": "evt_duplicate"})
                receipt = {"message_id": "msg_owned", "state": "waiting", "replaced": False}
                with mock.patch.object(edge, "send", return_value=({"text": a["waiting_messages"][0]["text"]}, receipt)), \
                     mock.patch.object(edge, "agent", return_value=a), mock.patch.object(edge, "events", return_value=evs):
                    if invalid is None:
                        edge.large_send("hundred-k")
                    else:
                        with self.assertRaises(AssertionError):
                            edge.large_send("hundred-k")
                saved = edge.load("large", "hundred-k-precondition")
                self.assertEqual(saved["running_turn_id"], a["running_turn_id"])
                self.assertEqual(saved["ask_ids"], [a["open_asks"][0]["id"]])
                self.assertNotIn("PRIVATE_COMMAND", (edge.OUT / "large-hundred-k-precondition.json").read_text())
                self.assertEqual((edge.OUT / "large-hundred-k.json").exists(), invalid is None)

    def interrupt_fixture(self, edge, directory):
        edge.OUT = Path(directory)
        (edge.OUT / "interrupt.id").write_text("agt_owned\n")
        edge.save("interrupt", "identity", {"agent_id": "agt_owned"})
        edge.save("interrupt", "running", {"turn_id": "turn_original"})
        old = {"body": {"text": "PRIVATE_OLD_TEXT"}, "key": "old-request",
               "receipt": {"message_id": "msg_old", "state": "waiting", "replaced": False}}
        new = {"body": {"text": "PRIVATE_NEW_TEXT"}, "key": "rs-edges-offline-interrupt-new",
               "receipt": {"message_id": "msg_new", "state": "waiting", "replaced": True,
                           "interrupted": True}}
        edge.save("interrupt", "old", old)
        return old, new

    def test_replace_interrupt_records_safe_mismatch_before_original_assertion(self):
        for stage in ("handed", "wrong-id", "missing", "null"):
            with self.subTest(stage=stage), tempfile.TemporaryDirectory() as directory:
                edge = module()
                old, new = self.interrupt_fixture(edge, directory)
                new["receipt"]["authorization"] = "PRIVATE_TOKEN"
                retries = {
                    "handed": {**new["receipt"], "state": "handed"},
                    "wrong-id": {**new["receipt"], "message_id": "msg_foreign"},
                    "missing": {k: v for k, v in new["receipt"].items() if k != "state"},
                    "null": None,
                }
                with mock.patch.object(edge, "send", return_value=(new["body"], new["receipt"])), \
                     mock.patch.object(edge, "agent_path", return_value="/owned/agent"), \
                     mock.patch.object(edge, "events", return_value=[]), \
                     mock.patch.object(edge, "call", side_effect=[old["receipt"], retries[stage]]):
                    with self.assertRaises(AssertionError):
                        edge.replace_interrupt()
                initial = json.loads((edge.OUT / "interrupt-new-initial-receipt.json").read_text())
                observed = json.loads((edge.OUT / "interrupt-replace-new-replay.json").read_text())
                self.assertEqual(initial["original_request_id"], new["key"])
                self.assertNotIn("retry", initial)
                self.assertEqual((observed["agent_id"], observed["running_turn_id"]),
                                 ("agt_owned", "turn_original"))
                self.assertEqual((observed["original_request_id"], observed["retry_request_id"]),
                                 (new["key"], new["key"]))
                self.assertEqual(observed["original"]["fields"]["message_id"], "msg_new")
                if stage == "handed":
                    self.assertEqual(observed["retry"]["fields"]["state"], "handed")
                elif stage == "wrong-id":
                    self.assertEqual(observed["retry"]["fields"]["message_id"], "msg_foreign")
                elif stage == "missing":
                    self.assertEqual(observed["retry"]["missing_required"], ["state"])
                else:
                    self.assertEqual(observed["retry"]["shape"], "null")
                for file in ("interrupt-new-initial-receipt.json", "interrupt-replace-old-replay.json",
                             "interrupt-replace-new-replay.json"):
                    self.assertNotIn("PRIVATE_", (edge.OUT / file).read_text())
                self.assertFalse((edge.OUT / "interrupt-new.json").exists())

    def test_replace_interrupt_equal_replays_keep_success_oracle(self):
        edge = module()
        with tempfile.TemporaryDirectory() as directory:
            old, new = self.interrupt_fixture(edge, directory)
            with mock.patch.object(edge, "send", return_value=(new["body"], new["receipt"])), \
                 mock.patch.object(edge, "agent_path", return_value="/owned/agent"), \
                 mock.patch.object(edge, "call", side_effect=[old["receipt"], new["receipt"]]) as public:
                edge.replace_interrupt()
            self.assertEqual(public.call_count, 2)
            self.assertEqual(edge.load("interrupt", "new")["new_receipt"], new["receipt"])
            observed = edge.load("interrupt", "replace-new-replay")
            self.assertEqual(observed["original"], observed["retry"])

    def test_immediate_handed_interrupt_requires_matching_saved_delivery(self):
        for stage in ("matching", "missing", "wrong-key", "wrong-text"):
            with self.subTest(stage=stage), tempfile.TemporaryDirectory() as directory:
                edge = module()
                old, new = self.interrupt_fixture(edge, directory)
                key = edge.native_key("interrupt", new["key"])
                event = {"kind": "message.delivered", "event_id": "evt_delivered", "turn_id": "turn_new",
                         "payload": {"text": new["body"]["text"], "inputKey": key}}
                if stage == "wrong-key":
                    event["payload"]["inputKey"] = "msg_foreign"
                elif stage == "wrong-text":
                    event["payload"]["text"] = "PRIVATE_OTHER"
                evs = [] if stage == "missing" else [event]
                retry = {**new["receipt"], "state": "handed"}
                with mock.patch.object(edge, "send", return_value=(new["body"], new["receipt"])), \
                     mock.patch.object(edge, "agent_path", return_value="/owned/agent"), \
                     mock.patch.object(edge, "events", return_value=evs), \
                     mock.patch.object(edge, "call", side_effect=[old["receipt"], retry]):
                    if stage == "matching":
                        edge.replace_interrupt()
                    else:
                        with self.assertRaises(AssertionError):
                            edge.replace_interrupt()
                self.assertEqual((edge.OUT / "interrupt-new.json").exists(), stage == "matching")

    def test_interrupt_after_retains_replay_evidence_and_native_once_oracle(self):
        for retry_state in ("handed", "waiting"):
            with self.subTest(retry_state=retry_state), tempfile.TemporaryDirectory() as directory:
                edge = module()
                old, new = self.interrupt_fixture(edge, directory)
                edge.save("interrupt", "new", new)
                input_key = edge.native_key("interrupt", new["key"])
                evs = [
                    {"kind": "agent.turn_completed", "turn_id": "turn_original", "seq": 1,
                     "event_id": "evt_cancel", "payload": {"stopReason": "cancelled"}},
                    {"kind": "turn.started", "turn_id": "turn_new", "seq": 2,
                     "event_id": "evt_start", "payload": {}},
                    {"kind": "message.delivered", "turn_id": "turn_new", "seq": 3,
                     "event_id": "evt_delivered", "payload": {"text": new["body"]["text"], "inputKey": input_key}},
                    {"kind": "agent.turn_completed", "turn_id": "turn_new", "seq": 4,
                     "event_id": "evt_completed", "payload": {"stopReason": "completed"}},
                ]
                retry = {**new["receipt"], "state": retry_state}
                with mock.patch.object(edge, "events", return_value=evs), \
                     mock.patch.object(edge, "agent", return_value={"waiting_messages": []}), \
                     mock.patch.object(edge, "event_ids", return_value=[e["event_id"] for e in evs]), \
                     mock.patch.object(edge, "agent_path", return_value="/owned/agent"), \
                     mock.patch.object(edge, "call", side_effect=[old["receipt"], retry]), \
                     mock.patch.object(edge, "native", return_value={"native_user_message_count": 1}) as native:
                    edge.interrupt_after()
                    native.assert_called_once_with("count", "interrupt", input_key)
                observed = json.loads((edge.OUT / "interrupt-after-new-replay.json").read_text())
                self.assertEqual(observed["retry"]["fields"]["state"], retry_state)
                self.assertEqual(observed["original_request_id"], new["key"])
                self.assertEqual(observed["retry_request_id"], new["key"])
                self.assertTrue((edge.OUT / "interrupt-delivered.json").exists())

    def test_interrupt_replay_rejects_wrong_public_fields_and_mutation(self):
        edge = module()
        original = {"message_id": "msg_new", "state": "waiting", "replaced": True, "interrupted": True}
        delivered = {"event_id": "evt_delivered", "payload": {"inputKey": "msg_exact"}}
        for retry in ({**original, "state": "handed", "message_id": "msg_foreign"},
                      {**original, "state": "handed", "replaced": False},
                      {**original, "state": "handed", "interrupted": False},
                      {**original, "state": "handed", "secret": "PRIVATE_TOKEN"},
                      {key: value for key, value in original.items() if key != "replaced"},
                      {**original, "state": "active"}):
            with self.subTest(retry=retry):
                self.assertFalse(edge.same_interrupt_receipt(original, retry, delivered))
        self.assertTrue(edge.same_interrupt_receipt(original, {**original, "state": "handed"}, delivered))
        self.assertFalse(edge.same_interrupt_receipt(original, {**original, "state": "handed"}))

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
