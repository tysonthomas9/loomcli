#!/usr/bin/env python3
"""Offline negative fixtures for the live child-queue evidence predicates."""

import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("queue", Path(__file__).with_name("coverage-children-queue.py"))
queue = importlib.util.module_from_spec(spec)
spec.loader.exec_module(queue)


def delivery(seq, text, sender, key, child="agt_child"):
    return {"agent_id": child, "kind": "message.delivered", "seq": seq,
            "event_id": f"delivered:{key}", "payload": {"text": text, "sender": sender, "inputKey": key}}


class QueueOracleTests(unittest.TestCase):
    def test_two_authentic_sender_slots_and_fifo_position(self):
        rows = [{"sender": "user:local", "text": "U2", "since": "t1"},
                {"sender": "agent:agt_lead", "text": "P1", "since": "t2"}]
        self.assertEqual(queue.sender_pair(rows, "U2", "P1", "agt_lead"), ("user:local", "agent:agt_lead"))
        for bad in (
            list(reversed(rows)),
            [{**rows[0], "sender": "agent:agt_lead"}, rows[1]],
            [rows[0], {**rows[1], "sender": "user:local"}],
            [rows[0], {**rows[1], "sender": "agent:agt_foreign"}],
        ):
            with self.assertRaises(AssertionError):
                queue.sender_pair(bad, "U2", "P1", "agt_lead")

    def test_delivery_requires_order_once_and_distinct_input_keys(self):
        rows = [delivery(10, "U2", "user:local", "in-u"),
                delivery(12, "P1", "agent:agt_lead", "in-p")]
        self.assertEqual(len(queue.delivery_pair(rows, "U2", "user:local", "P1", "agent:agt_lead", ("U1",))), 2)
        for bad in (
            [rows[1], rows[0]],
            [rows[0], {**rows[1], "seq": 9}],
            [rows[0], {**rows[1], "payload": {**rows[1]["payload"], "sender": "user:local"}}],
            [rows[0], {**rows[1], "payload": {**rows[1]["payload"], "inputKey": "in-u"}}],
            rows + [delivery(13, "P1", "agent:agt_lead", "in-duplicate")],
            rows + [delivery(14, "U1", "user:local", "in-superseded")],
        ):
            with self.assertRaises(AssertionError):
                queue.delivery_pair(bad, "U2", "user:local", "P1", "agent:agt_lead", ("U1",))

    def test_native_call_requires_completed_tool_and_exact_target(self):
        event = {"kind": "item.completed", "payload": {"itemKind": "tool", "tool": {
            "name": "execute", "input": {"code": 'await tools.loom.agent_send({agent:"agt_child",text:"QUEUE-P1"})'}}}}
        self.assertTrue(queue.actual_call(event, "agent_send", "agt_child", "QUEUE-P1"))
        for bad in (
            {**event, "kind": "item.started"},
            {"kind": "item.completed", "payload": {**event["payload"], "itemKind": "message"}},
            {"kind": "item.completed", "payload": {"itemKind": "tool", "tool": {
                "name": "execute", "input": {"code": 'await tools.loom.agent_get({agent:"agt_child"}) // QUEUE-P1'}}}},
        ):
            self.assertFalse(queue.actual_call(bad, "agent_send", "agt_child", "QUEUE-P1"))
        self.assertFalse(queue.actual_call(event, "agent_send", "agt_foreign", "QUEUE-P1"))

    def test_native_parent_replacement_receipt_must_be_true(self):
        first = {"event_id": "one", "turn_id": "turn-one", "kind": "item.completed", "payload": {
            "itemKind": "tool", "tool": {"output": '{"replaced":false,"message_id":"m1","state":"waiting"}'}}}
        second = {"event_id": "two", "turn_id": "turn-two", "kind": "item.completed", "payload": {
            "itemKind": "tool", "tool": {"output": '{"replaced":true,"message_id":"m2","state":"waiting"}'}}}
        self.assertEqual(queue.replacement_result(first, second)[1]["message_id"], "m2")
        for bad in ({**second, "payload": {"itemKind": "tool", "tool": {"output": '{"replaced":false,"message_id":"m2","state":"waiting"}'}}},
                    {**second, "payload": {"itemKind": "tool", "tool": {"output": '{}'}}},
                    {**second, "payload": {"itemKind": "tool", "tool": {"output": 'replaced:true'}}},
                    {**second, "payload": {"itemKind": "tool", "tool": {"output": '{"replaced":true,"message_id":"m1","state":"waiting"}'}}}):
            with self.assertRaises(AssertionError):
                queue.replacement_result(first, bad)
        with self.assertRaises(AssertionError):
            queue.replacement_result(first, {**second, "turn_id": "turn-one"})

    def test_shot_rows_require_exact_waiting_count_order_and_first(self):
        u2, p1 = queue.TEXT["u2"], queue.TEXT["p1"]
        ui = {"waiting": ["Waiting " + u2, "Waiting from Lead " + p1], "history": [], "user": []}
        api = [{"text": u2, "sender": "user:local"}, {"text": p1, "sender": "agent:agt_lead"}]
        queue.assert_shot_rows("fifo-replaced", ui, api, "agt_lead")
        for bad_ui, bad_api in (({**ui, "waiting": list(reversed(ui["waiting"]))}, api),
                                (ui, list(reversed(api))),
                                ({**ui, "waiting": ui["waiting"][:1]}, api),
                                ({**ui, "history": [queue.TEXT["u1"]]}, api)):
            with self.assertRaises(AssertionError):
                queue.assert_shot_rows("fifo-replaced", bad_ui, bad_api, "agt_lead")
        first_ui = {"waiting": ["Waiting " + queue.TEXT["p3b"]],
                    "history": [queue.TEXT["u3"]], "user": [queue.TEXT["u3"]]}
        queue.assert_shot_rows("first-user-interrupt", first_ui, [{"text": queue.TEXT["p3b"], "sender": "agent:agt_lead"}], "agt_lead")
        with self.assertRaises(AssertionError):
            queue.assert_shot_rows("first-user-interrupt", {**first_ui, "user": []}, [{"text": queue.TEXT["p3b"], "sender": "agent:agt_lead"}], "agt_lead")
        queue.assert_shot_rows("first-parent-initial", {"waiting": [queue.TEXT["p3"]], "history": [], "user": []},
                               [{"text": queue.TEXT["p3"], "sender": "agent:agt_lead"}], "agt_lead")

    def test_owned_ref_probe_rejects_foreign_identity(self):
        with patch.dict(os.environ, {"AFT_TESTS_DIR": "/tmp"}), patch.object(queue.subprocess, "check_output", return_value=json.dumps({
            "agent_id": "agt_foreign", "kind": "child", "branch": "child", "head": "a" * 40})):
            with self.assertRaises(AssertionError):
                queue.native_ref("child", "agt_child", "b" * 40)

    def test_screenshot_route_rejects_foreign_child(self):
        queue.assert_shot_route("/ws/LOCALMODE/chat/agt_child", "agt_child", "LOCALMODE")
        with self.assertRaises(AssertionError):
            queue.assert_shot_route("/ws/LOCALMODE/chat/agt_foreign", "agt_child", "LOCALMODE")

    def test_u3_exact_request_replay_rejects_new_event_or_changed_receipt(self):
        receipt = {"request_id": "request-u3", "body": {"text": queue.TEXT["u3"], "delivery": "interrupt"},
                   "result": {"message_id": "m3", "state": "waiting", "replaced": False, "interrupted": True}}
        proof = {"delivered": [{"input_key": "msg_" + "a" * 26}, {"input_key": "msg_" + "b" * 26}]}
        saved = {"receipt-u3": receipt, "first-delivery": proof}
        row = {"state": "finished", "attempt": 1, "waiting_messages": []}
        event = {"seq": 10, "event_id": "one"}
        with patch.object(queue, "load", side_effect=saved.__getitem__), patch.object(queue, "identity", return_value="agt_child"), \
             patch.object(queue, "agent", return_value=row), patch.object(queue, "events", side_effect=[[event], [event]]), \
             patch.object(queue, "http", return_value=receipt["result"]) as post, patch.object(queue, "save"):
            queue.replay_u3()
            self.assertEqual(post.call_args.args[2], receipt["request_id"])
        for replay, after in (({**receipt["result"], "replaced": True}, [event]),
                              (receipt["result"], [event, {"seq": 11, "event_id": "extra"}])):
            with patch.object(queue, "load", side_effect=saved.__getitem__), patch.object(queue, "identity", return_value="agt_child"), \
                 patch.object(queue, "agent", return_value=row), patch.object(queue, "events", side_effect=[[event], after]), \
                 patch.object(queue, "http", return_value=replay), patch.object(queue, "save"):
                with self.assertRaises(AssertionError):
                    queue.replay_u3()

    def test_native_input_probe_rejects_missing_or_duplicate_count(self):
        keys = ["msg_" + "a" * 26, "msg_" + "b" * 26]
        proof = {"delivered": [{"input_key": key} for key in keys]}
        def reply(args, text):
            key = args[-1]
            return json.dumps({"agent_id": "agt_child", "harness": "opencode", "input_key": key,
                               "native_id": "ses_child", "native_root": "/owned",
                               "native_user_message_count": 2 if key == keys[1] else 1})
        with patch.dict(os.environ, {"AFT_TESTS_DIR": "/tmp"}), patch.object(queue, "load", return_value=proof), \
             patch.object(queue, "identity", return_value="agt_child"), \
             patch.object(queue.subprocess, "check_output", side_effect=reply), patch.object(queue, "save"):
            with self.assertRaisesRegex(AssertionError, "exact input once"):
                queue.native_inputs()

    def test_native_probe_cli_rejects_compose_only_exec_flag(self):
        source = Path(__file__).with_name("coverage-children-queue-native.sh").read_text()
        line = next(x.strip() for x in source.splitlines() if x.startswith('agent_flows_podman exec '))
        prefix = line.split(' node -e ', 1)[0]
        check = ('agent_flows_podman() { [[ "$1" == exec && "$2" == owned && "$3" == /bin/true ]]; }; '
                 'container=owned; ' + prefix + ' /bin/true')
        self.assertEqual(subprocess.run(["bash", "-c", check], capture_output=True).returncode, 0)
        bad = check.replace(' exec "$container" ', ' exec -T "$container" ')
        self.assertNotEqual(subprocess.run(["bash", "-c", bad], capture_output=True).returncode, 0)

    def test_native_probe_accepts_empty_root_but_rejects_missing_root(self):
        source = Path(__file__).with_name("coverage-children-queue-native.sh").read_text()
        match = re.search(r'(const validNativeRow=\(row,run\)=>[\s\S]*?;)\n  if\(!validNativeRow', source)
        self.assertIsNotNone(match)
        script = match.group(1) + '''
const row={name:"cov-child-queue-task-af12345678",harness:"opencode",preset:"task",
 parent_agent_id:"agt_lead",root_agent_id:"agt_lead",created_by_id:"agt_lead",
 worktree_path:"/owned/child",native_id:"ses_child",native_root:""};
if(!validNativeRow(row,"af12345678") || validNativeRow({...row,native_root:null},"af12345678") ||
   validNativeRow({...row,created_by_id:"agt_foreign"},"af12345678")) process.exit(1);
'''
        result = subprocess.run(["node", "-e", script], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_event_pager_rejects_foreign_child_and_duplicate_event(self):
        expected = {"agent_id": "agt_child", "seq": 1, "event_id": "one", "kind": "message.waiting", "payload": {}}
        with patch.object(queue, "identity", return_value="agt_child"), patch.object(queue, "http", return_value={
            "snapshot_seq": 1, "events": [{**expected, "agent_id": "agt_foreign"}], "more": False}):
            with self.assertRaises(AssertionError):
                queue.events("child")
        with patch.object(queue, "identity", return_value="agt_child"), patch.object(queue, "http", return_value={
            "snapshot_seq": 2, "events": [expected, {**expected, "seq": 2}], "more": False}):
            with self.assertRaises(AssertionError):
                queue.events("child")


if __name__ == "__main__":
    unittest.main()
