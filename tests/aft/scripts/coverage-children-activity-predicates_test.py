#!/usr/bin/env python3
"""Offline regressions for event-backed child completion predicates."""

import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


os.environ.setdefault("RUN_ID", "af12345678")
os.environ.setdefault("AFT_WS", "LOCALMODE")
os.environ.setdefault("AFT_API_URL", "http://127.0.0.1:1")
os.environ.setdefault("AFT_WORK_DIR", tempfile.mkdtemp(prefix="coverage-child-predicates-"))
spec = importlib.util.spec_from_file_location("child_oracles", Path(__file__).with_name("coverage-children-activity.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def event(kind, seq, event_id, payload=None, turn_id=""):
    return {"kind": kind, "seq": seq, "event_id": event_id, "turn_id": turn_id, "payload": payload or {}}


class ChildProofPredicates(unittest.TestCase):
    def setUp(self):
        self.a, self.b = "agt_pairA", "agt_pairB"
        self.records = [event("task_completed", 11, f"task_completed:{self.a}:0", {"outcome": "completed", "head": "abc"}),
                        event("task_completed", 12, f"task_completed:{self.b}:0", {"outcome": "completed", "head": "abc"})]
        self.delivery = event("message.delivered", 21, "delivered", {"completions": [
            {"child": self.a, "attempt": 0}, {"child": self.b, "attempt": 0}]})
        self.history = self.records + [event("agent.turn_completed", 20, "busy-end", turn_id="turn-busy"),
                                       self.delivery,
                                       event("item.completed", 22, "reply", {"itemKind": "message", "text": "pair-a pair-b done"})]
        self.labels = {"pair-lead": {"agent_id": "agt_lead"},
                       "busy": {"children": [self.a, self.b], "lead_turn": "turn-busy", "last_lead_seq": 10},
                       "pair-a": {"agent_id": self.a, "name": "pair-a"},
                       "pair-b": {"agent_id": self.b, "name": "pair-b"}}

    def run_pair(self):
        with patch.object(module, "load", side_effect=lambda label: self.labels[label]), \
             patch.object(module, "events", side_effect=lambda agent_id: self.history if agent_id == "agt_lead" else [
                 event("item.completed", 2, "answer", {"itemKind": "message", "text": "done"}),
                 event("agent.turn_completed", 3, "finished")]), \
             patch.object(module, "agent", return_value={"base_ref": "main"}), \
             patch.object(module, "get", side_effect=lambda url: {"data": {
                 "files": [{"path": f"aft-child-fixtures/{module.RUN}/pair-{'a' if self.a in url else 'b'}.txt"}]}}
                 if "/files?" in url else {"data": {"commits": [{"hash": "abc"}]}}), \
             patch.object(module, "save"):
            module.pair("pair-lead", "pair-a", "pair-b")

    def test_qualified_tool_name_is_not_missed(self):
        tool = event("item.completed", 1, "tool", {"itemKind": "tool", "tool": {"name": "loom.agent_get"}})
        self.assertEqual(module.tool_name(tool), "agent_get")

    def test_execute_body_detects_real_loom_call_without_matching_brief_text(self):
        call = event("item.completed", 1, "tool", {"itemKind": "tool", "tool": {
            "name": "execute", "input": json.dumps({"code": 'return await tools.loom.agent_get({id:"agt_x"})'})}})
        brief = event("item.completed", 2, "brief", {"itemKind": "tool", "tool": {
            "name": "execute", "input": json.dumps({"code": 'return await tools.loom.agent_create({brief:"no agent_get"})'})}})
        self.assertTrue(module.calls_operation(call, "agent_get"))
        self.assertFalse(module.calls_operation(brief, "agent_get"))
        self.assertTrue(module.calls_operation(brief, "agent_create"))

    def test_one_grouped_handover_after_two_busy_completions(self):
        self.run_pair()

    def test_completion_after_busy_turn_is_rejected(self):
        self.records[1]["seq"] = 21
        with self.assertRaisesRegex(AssertionError, "while the Lead was busy"):
            self.run_pair()

    def test_duplicate_delivery_is_rejected(self):
        self.history.append(event("message.delivered", 23, "duplicate", {"completions": [
            {"child": self.a, "attempt": 0}]}))
        with self.assertRaises(AssertionError):
            self.run_pair()


if __name__ == "__main__":
    unittest.main()
