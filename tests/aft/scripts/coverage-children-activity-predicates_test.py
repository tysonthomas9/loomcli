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

    def test_preview_rejects_prior_turn(self):
        prior = event("item.completed", 1, "prior", {"itemKind": "tool", "tool": {"name": "bash"}}, "turn-old")
        prior["agent_id"] = self.a
        with self.assertRaisesRegex(AssertionError, "current-turn step"):
            module.current_preview_event([prior], self.a, "turn-current", lambda prefix: prefix == "▸ Ran command")

    def test_preview_rejects_wrong_child_even_with_matching_label(self):
        wrong = event("item.completed", 1, "wrong", {"itemKind": "tool", "tool": {"name": "bash"}}, "turn-current")
        wrong["agent_id"] = self.b
        with self.assertRaisesRegex(AssertionError, "current-turn step"):
            module.current_preview_event([wrong], self.a, "turn-current", lambda prefix: prefix == "▸ Ran command")

    def test_preview_requires_visible_product_step(self):
        right = event("item.completed", 1, "right", {"itemKind": "tool", "tool": {"name": "execute"}}, "turn-current")
        right["agent_id"] = self.a
        with self.assertRaisesRegex(AssertionError, "current-turn step"):
            module.current_preview_event([right], self.a, "turn-current", lambda prefix: prefix == "▸ Ran command")
        self.assertEqual(module.current_preview_event([right], self.a, "turn-current",
                                                      lambda prefix: prefix == "▸ Ran code")["event_id"], "right")

    def test_preview_rejects_older_step_in_same_turn(self):
        old = event("item.completed", 1, "old", {"itemKind": "tool", "tool": {"name": "bash"}}, "turn-current")
        latest = event("item.completed", 2, "latest", {"itemKind": "tool", "tool": {"name": "read"}}, "turn-current")
        old["agent_id"] = latest["agent_id"] = self.a
        with self.assertRaisesRegex(AssertionError, "latest saved current-turn step"):
            module.current_preview_event([old, latest], self.a, "turn-current", lambda prefix: prefix == "▸ Ran command")

    def test_switched_ref_rejects_wrong_saved_original_branch(self):
        original = "assigned-child-branch"
        actual = {"branch": f"cov-child-switched-{module.RUN}", "head": "a" * 40,
                  "changed": [f"aft-child-fixtures/{module.RUN}/second.txt"]}
        module.switched_ref({"branch": original}, {"branch": original, "head": None}, original, actual)
        with self.assertRaisesRegex(AssertionError, "original branch"):
            module.switched_ref({"branch": original}, {"branch": "other", "head": None}, original, actual)
        with self.assertRaisesRegex(AssertionError, "original branch"):
            module.switched_ref({"branch": "other"}, {"branch": original, "head": None}, original, actual)

    def test_mobile_tray_geometry_rejects_clipping_and_overlap(self):
        good = {"width": 390, "height": 844, "theme": "dark", "navPosition": "fixed",
                "trayOpen": True, "childId": self.a, "headerExpanded": True,
                "rowIds": [self.a], "rowCount": 1, "visibleRowCount": 1,
                "childWhole": True,
                "headerHit": {"x": 180, "y": 480, "hit": True, "target": "SPAN"},
                "childLinkHit": {"x": 310, "y": 540, "hit": True, "target": "A"},
                "partialRows": 0, "hiddenRows": 0, "moreCount": 0,
                "horizontalOverflow": 0, "maxControlBottom": 600,
                "composerTop": 610, "composerBottom": 770, "navTop": 780}
        module.mobile_geometry_ok(good, self.a, 390, "dark")
        for change, message in [({"partialRows": 1}, "clipped"),
                                ({"horizontalOverflow": 3}, "overflows"),
                                ({"maxControlBottom": 620}, "usable composer"),
                                ({"composerBottom": 790}, "mobile navigation"),
                                ({"hiddenRows": 1}, "More count"),
                                ({"childWhole": False}, "exact saved child row"),
                                ({"headerHit": {"x": 180, "y": 480, "hit": False, "target": "DIV"}}, "tray header"),
                                ({"childLinkHit": {"x": 310, "y": 540, "hit": False, "target": "DIV"}}, "child Open link")]:
            with self.subTest(change=change), self.assertRaisesRegex(AssertionError, message):
                module.mobile_geometry_ok({**good, **change}, self.a, 390, "dark")


if __name__ == "__main__":
    unittest.main()
