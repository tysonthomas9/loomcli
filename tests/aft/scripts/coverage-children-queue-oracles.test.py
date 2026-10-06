#!/usr/bin/env python3
"""Offline negative fixtures for the live child-queue evidence predicates."""

import importlib.util
from pathlib import Path
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
        first = {"event_id": "one", "payload": {"tool": {"output": '{"replaced": false}'}}}
        second = {"event_id": "two", "payload": {"tool": {"output": '{"replaced": true}'}}}
        queue.replacement_result(first, second)
        queue.replacement_result({"event_id": "combined", "payload": {"tool": {
            "output": '{"first":{"replaced":false},"second":{"replaced":true}}'}}},
            {"event_id": "combined", "payload": {"tool": {
            "output": '{"first":{"replaced":false},"second":{"replaced":true}}'}}})
        for bad in ({**second, "payload": {"tool": {"output": '{"replaced": false}'}}},
                    {**second, "payload": {"tool": {"output": '{}'}}}):
            with self.assertRaises(AssertionError):
                queue.replacement_result(first, bad)
        combined_bad = {"event_id": "combined", "payload": {"tool": {
            "output": '{"first":{"replaced":true},"second":{"replaced":false}}'}}}
        with self.assertRaises(AssertionError):
            queue.replacement_result(combined_bad, combined_bad)

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
