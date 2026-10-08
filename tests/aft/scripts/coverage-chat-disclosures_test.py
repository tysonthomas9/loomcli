#!/usr/bin/env python3
"""Offline positive and refusal cases; these are not live AFT evidence."""

from copy import deepcopy
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("disclosures", Path(__file__).with_name("coverage-chat-disclosures.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def rect(x=0, y=0, width=300, height=32):
    return {"x": x, "y": y, "width": width, "height": height}


def actions():
    stage = {"copy": True, "time": "8:55 PM", "path": "/ws/owned/chat/agt_lead",
             "text": "Full saved reply", "opacity": "0", "pointerEvents": "none",
             "hovered": False, "focused": False, "viewportHeight": 700,
             "row": rect(height=154), "markdown": rect(height=120),
             "pill": rect(y=124, width=100, height=26),
             "button": rect(x=78, y=129, width=14, height=16)}
    stages = {name: deepcopy(stage) for name in ("rest", "hover", "leave", "focus")}
    for name in ("hover", "focus"):
        stages[name].update(opacity="1", pointerEvents="auto")
    stages["hover"]["hovered"] = True
    stages["focus"]["focused"] = True
    return stages


def reasoning(text="Plan\nDetails"):
    item = {"event_id": "reasoning-owned", "kind": "item.completed",
            "payload": {"itemKind": "reasoning", "text": text}}
    available = bool(text and text.strip())
    row = {"heading": "Thinking", "status": "completed",
           "preview": "Plan" if available else "No reasoning text available",
           "body": None, "expanded": "false" if available else None,
           "geometry": {"row": rect(width=280), "parent": rect(width=600),
                        "chevron": rect(x=256, width=16, height=24), "tabIndex": 0}}
    return item, row


def started():
    return {"path": "/ws/owned/chat/agt_lead", "width": 350, "scrollWidth": 350,
            "markers": [{"row": rect(width=350, height=70), "parent": rect(width=350, height=70),
                         "links": [{"href": "/ws/owned/chat/agt_child", "rect": rect(x=70, width=270),
                                    "radius": 8, "tabIndex": 0}],
                         "buttons": [{"rect": rect(y=36, width=85), "radius": 8,
                                      "tabIndex": 0, "expanded": "false"}]}]}


class Disclosures(unittest.TestCase):
    def test_agent_hover_command_captures_real_action_stages_in_both_themes(self):
        with tempfile.TemporaryDirectory() as folder, patch.dict(os.environ, {
            "AFT_WORK_DIR": folder, "AFT_WS": "owned", "RUN_ID": "offline",
            "AFT_API_URL": "http://127.0.0.1:1",
        }):
            helper_spec = importlib.util.spec_from_file_location("visual_disclosure_test", Path(__file__).with_name("coverage-chat-visual.py"))
            helper = importlib.util.module_from_spec(helper_spec)
            helper_spec.loader.exec_module(helper)
            state = {"stage": "rest", "theme": "light"}
            receipts = []

            def browser(*args):
                if args[:2] == ("mouse", "move"):
                    state["stage"] = "hover"
                elif args == ("hover", "form"):
                    state["stage"] = "leave" if state["stage"] == "hover" else "rest"
                elif args[0] == "click":
                    state["theme"] = "dark" if "dark" in args[1] else "light"

            def evaluate(script):
                if script == helper.REPLY_ACTIONS_DOM:
                    return actions()[state["stage"]]
                if script == "document.documentElement.dataset.theme":
                    return state["theme"]
                if "document.activeElement?.blur()" in script:
                    state["stage"] = "rest"
                    return True
                if "b.focus()" in script:
                    state["stage"] = "focus"
                    return True
                if "flatMap" in script:
                    return [20]
                self.fail(f"unexpected browser read: {script}")

            with patch.object(helper, "current"), patch.object(helper, "browser", side_effect=browser), \
                 patch.object(helper, "evaluate", side_effect=evaluate), patch.object(helper, "shot"), \
                 patch.object(helper, "write", side_effect=lambda name, value: receipts.append((name, value))), \
                 patch.object(sys, "argv", ["coverage-chat-visual.py", "agent-hover"]):
                helper.main()
            self.assertEqual(receipts[0][0], "render-agent-hover.json")
            self.assertEqual([item["theme"] for item in receipts[0][1]], ["light", "dark"])
            for item in receipts[0][1]:
                module.assert_reply_actions(item["stages"])

    def test_full_reply_crosses_former_limit_and_retains_tail(self):
        source = "word " * 3400 + "😀 full tail"
        projection = {"mode": "terminal-full", "version": 1,
                      "sourceUtf16": len(source.encode("utf-16-le")) // 2, "terminal": source}
        module.assert_full_reply(source, {"answer": source, "showAll": False}, projection)
        for dom in ({"answer": source[:8000], "showAll": False},
                    {"answer": source, "showAll": True}, {"answer": "foreign", "showAll": False}):
            with self.subTest(dom_length=len(dom["answer"])), self.assertRaises(AssertionError):
                module.assert_full_reply(source, dom, projection)
        with self.assertRaises(AssertionError):
            module.assert_full_reply(source, {"answer": source, "showAll": False}, projection | {"sourceUtf16": 8000})

    def test_reply_controls_hide_hover_leave_focus_without_layout_shift(self):
        module.assert_reply_actions(actions())

    def test_reply_controls_refuse_missing_visibility_focus_and_geometry(self):
        changes = [("rest", {"opacity": "1"}), ("rest", {"pointerEvents": "auto"}),
                   ("hover", {"copy": False}), ("hover", {"time": ""}),
                   ("hover", {"hovered": False}), ("focus", {"focused": False}),
                   ("leave", {"opacity": "1"}), ("focus", {"text": "Other reply"}),
                   ("hover", {"path": "/ws/foreign/chat/agt_other"}),
                   ("hover", {"pill": rect(y=100, width=100)}),
                   ("hover", {"row": rect(width=280, height=154)}),
                   ("focus", {"viewportHeight": 140}), ("rest", {"markdown": None})]
        for name, change in changes:
            with self.subTest(name=name, change=change), self.assertRaises(AssertionError):
                candidate = actions()
                candidate[name].update(change)
                module.assert_reply_actions(candidate)
        with self.assertRaises(AssertionError):
            module.assert_reply_actions({"rest": actions()["rest"]})

    def test_thinking_saved_text_collapses_and_expands_exactly(self):
        item, row = reasoning()
        module.assert_thinking_presentation([item], [row], False)
        row.update(expanded="true", body=item["payload"]["text"])
        module.assert_thinking_presentation([item], [row], True)

    def test_empty_reasoning_is_explicit_and_nonexpandable(self):
        for text in ("", "  ", None):
            item, row = reasoning(text)
            module.assert_thinking_presentation([item], [row], False)
            module.assert_thinking_presentation([item], [row], True)
        item, row = reasoning(None)
        del item["payload"]["text"]
        module.assert_thinking_presentation([item], [row], False)

    def test_empty_reasoning_refuses_invented_text_or_toggle(self):
        item, row = reasoning("")
        for change in ({"preview": "Plan"}, {"expanded": "false"}, {"body": "Invented"}):
            with self.subTest(change=change), self.assertRaises(AssertionError):
                module.assert_thinking_presentation([item], [row | change], False)

    def test_thinking_refuses_missing_foreign_and_detached_disclosure(self):
        item, row = reasoning()
        bad_geometry = [{"row": rect(width=700)}, {"row": rect(height=20)},
                        {"chevron": rect(x=150, width=16)}, {"tabIndex": -1}]
        for change in bad_geometry:
            with self.subTest(change=change), self.assertRaises(AssertionError):
                candidate = deepcopy(row)
                candidate["geometry"].update(change)
                module.assert_thinking_presentation([item], [candidate], False)
        for change in ({"event_id": ""}, {"kind": "tool.started"}, {"payload": {"itemKind": "message"}}):
            with self.subTest(change=change), self.assertRaises(AssertionError):
                module.assert_thinking_presentation([item | change], [row], False)
        with self.assertRaises(AssertionError):
            module.assert_thinking_presentation([item], [], False)
        with self.assertRaises(AssertionError):
            module.assert_thinking_presentation([item], [row | {"expanded": "true", "body": "Wrong"}], True)

    def test_started_chips_wrap_without_overflow_and_link_exact_children(self):
        module.assert_started_layout(started(), "owned", "agt_lead", ["agt_child"])

    def test_started_refuses_foreign_missing_overflow_and_noninteractive_controls(self):
        for change in ({"path": "/ws/foreign/chat/agt_lead"}, {"markers": []}, {"scrollWidth": 500}):
            with self.subTest(change=change), self.assertRaises(AssertionError):
                module.assert_started_layout(started() | change, "owned", "agt_lead", ["agt_child"])
        for control, change in (("links", {"href": "/ws/owned/chat/agt_other"}),
                                ("links", {"radius": 0}), ("links", {"tabIndex": -1}),
                                ("buttons", {"radius": 0}), ("buttons", {"tabIndex": -1}),
                                ("buttons", {"expanded": None}),
                                ("links", {"rect": rect(x=300, width=100)})):
            with self.subTest(control=control, change=change), self.assertRaises(AssertionError):
                candidate = started()
                candidate["markers"][0][control][0].update(change)
                module.assert_started_layout(candidate, "owned", "agt_lead", ["agt_child"])


if __name__ == "__main__":
    unittest.main()
