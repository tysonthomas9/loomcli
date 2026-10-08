#!/usr/bin/env python3
"""Offline negative checks for the live suite's evidence oracles."""

import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

for key, value in {
    "AFT_WORK_DIR": "/tmp/coverage-chat-controls-oracle",
    "RUN_ID": "oracle",
    "AFT_WS": "LOCALMODE",
    "AFT_API_URL": "http://127.0.0.1:1",
    "AFT_BASE_URL": "http://127.0.0.1:1",
    "AFT_REAL_MODEL": "openai/gpt-5.5",
}.items():
    os.environ.setdefault(key, value)

spec = importlib.util.spec_from_file_location(
    "coverage_chat_controls", Path(__file__).with_name("coverage-chat-controls.py")
)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class OracleTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        module.ROOT = Path(self.temp.name)

    def test_catalog_refuses_one_model_or_undeclared_effort(self):
        only = {"providers": [{"models": [{"id": module.MODEL, "source": "harness",
                   "option_descriptors": [{"id": "effort", "type": "select", "options": [{"id": "high"}]}]}]}]}
        with patch.object(module, "call", return_value=only):
            with self.assertRaisesRegex(AssertionError, "two real connected"):
                module.catalog()
        only["providers"][0]["models"].append({"id": "openai/another", "source": "harness"})
        only["providers"][0]["models"][0]["option_descriptors"] = []
        with patch.object(module, "call", return_value=only):
            with self.assertRaises(AssertionError):
                module.catalog()

    def test_turn_refuses_answer_without_delivered_request(self):
        with patch.object(module, "agent", return_value={"running_turn_id": None}), patch.object(module, "events", return_value=[
            {"kind": "agent.turn_completed", "seq": 2, "payload": {"stopReason": "completed"}}]):
            with self.assertRaisesRegex(AssertionError, "one real delivered"):
                module.turn("picker", "MARKER")

    def test_ask_refuses_unsaved_native_identity(self):
        a = {"open_asks": [{"id": "per_1", "type": "approval"}], "running_turn_id": "turn_1"}
        rows = [{"kind": "ask.opened", "turn_id": "turn_2", "event_id": "e1", "payload": {"askId": "per_1"}}]
        with patch.object(module, "agent", return_value=a), patch.object(module, "events", return_value=rows):
            with self.assertRaisesRegex(AssertionError, "another turn"):
                module.ask("approval")

    def test_ask_awaits_the_exact_saved_event_after_pending_snapshot(self):
        a = {"open_asks": [{"id": "per_1", "type": "approval"}], "running_turn_id": "turn_1"}
        row = {"kind": "ask.opened", "turn_id": "turn_1", "event_id": "e1", "payload": {"askId": "per_1"}}
        (module.ROOT / "denial.id").write_text("agt_abc\n")
        with patch.object(module, "agent", return_value=a), \
             patch.object(module, "events", side_effect=[[], [row]]), \
             patch.object(module, "browser") as browser:
            module.ask("denial", "declined")
        self.assertEqual(browser.call_args.args[:2], ("wait", "--fn"))
        self.assertIn('"per_1"', browser.call_args.args[2])
        self.assertEqual(module.saved("denial-declined-ask.json")["opened_event_id"], "e1")
        self.assertEqual(module.saved("denial-declined-pending.json")["initial_opened_event_ids"], [])

    def test_ask_refuses_two_saved_events_for_one_pending_id(self):
        a = {"open_asks": [{"id": "per_1", "type": "approval"}], "running_turn_id": "turn_1"}
        rows = [{"kind": "ask.opened", "turn_id": "turn_1", "event_id": eid,
                 "payload": {"askId": "per_1"}} for eid in ("e1", "e2")]
        with patch.object(module, "agent", return_value=a), patch.object(module, "events", return_value=rows):
            with self.assertRaisesRegex(AssertionError, "2 matching events"):
                module.ask("denial", "declined")

    def test_picker_receipt_refuses_foreign_chat_route_before_reading_prefs(self):
        (module.ROOT / "picker.id").write_text("agt_abc\n")
        with patch.object(module, "browser", return_value="http://127.0.0.1:1/ws/LOCALMODE/chat/agt_foreign") as browser:
            with self.assertRaisesRegex(AssertionError, "another Chat route or Agent ID"):
                module.picker_receipt("recent")
        browser.assert_called_once_with("get", "url")

    def test_recent_receipt_records_then_refuses_wrong_section_or_query(self):
        (module.ROOT / "picker.id").write_text("agt_abc\n")
        module.save("catalog.json", {"target": module.MODEL, "alternate": "openai/other"})
        base = {"route": f"/ws/{module.WS}/chat/agt_abc", "dialogOpen": True,
                "prefs": {"status": "parsed", "favorites": {"keys": [], "count": 0},
                          "recent": {"keys": [], "count": 0}},
                "optionIds": [module.MODEL], "optionCount": 1}
        agent = {"model": module.MODEL, "model_unverified": False}
        for section, query in (("favorites", ""), ("recent", "openai")):
            receipt = {**base, "selectedSection": section, "query": query, "queryLength": len(query)}
            with self.subTest(section=section, query=query), \
                 patch.object(module, "agent", return_value=agent), \
                 patch.object(module, "browser", side_effect=[
                     f"{os.environ['AFT_BASE_URL']}/ws/{module.WS}/chat/agt_abc", json.dumps(receipt)]):
                with self.assertRaisesRegex(AssertionError, "Recent picker section or empty search"):
                    module.picker_receipt("recent")
                self.assertEqual(module.saved("picker-recent-ui-prefs.json")["selectedSection"], section)

    def test_allow_requires_native_command_effect(self):
        with patch.object(module, "events", return_value=[{"kind": "ask.opened", "event_id": "e1", "payload": {}}]):
            with self.assertRaisesRegex(AssertionError, "executed commands"):
                module.approval_effect("approval", "MARKER", "1", "1")

    def test_resolved_ask_still_running_is_not_terminal(self):
        module.save("denial-declined-ask.json", {"ask_id": "per_1", "turn_id": "turn_1"})
        rows = [{"kind": "ask.resolved", "seq": 3, "turn_id": "turn_1", "event_id": "e3",
                 "payload": {"askId": "per_1"}},
                {"kind": "agent.turn_completed", "seq": 4, "turn_id": "turn_1", "event_id": "e4",
                 "payload": {"stopReason": "declined"}}]
        with patch.object(module, "agent", return_value={"running_turn_id": "turn_1", "open_asks": []}), \
             patch.object(module, "events", return_value=rows):
            with self.assertRaisesRegex(AssertionError, "active turn"):
                module.ask_terminal("denial", "declined", "declined")

    def test_active_icon_stop_makes_completion_wait_false(self):
        suites = Path(__file__).parents[1] / "live-agent-coverage-suites"
        source = "\n".join((suites / name).read_text() for name in
                           ("chat-controls-asks.test.yaml", "chat-controls-models.test.yaml"))
        old = "b.textContent === 'Stop'"
        guard = '!document.querySelector(\'form button[title="Stop the running turn"]\')'
        self.assertNotIn(old, source)
        self.assertGreaterEqual(source.count(guard), 9)
        script = "const document={querySelector:()=>({title:'Stop the running turn'})};" + \
                 f"if ({guard}) process.exit(1);"
        subprocess.run(["node", "-e", script], check=True)

    def test_reload_refuses_changed_saved_ask_content(self):
        original = {"event_id": "ask.opened::ses:per_1", "seq": 1,
                    "kind": "ask.opened", "turn_id": "turn_1",
                    "payload": {"askId": "per_1", "text": "owned command"}}
        with patch.object(module, "events", return_value=[original]), \
             patch.object(module, "aid", return_value="agt_a"):
            module.ask_history("approval", "once", "1,0,0")
        changed = {**original, "payload": {"askId": "per_1", "text": "changed command"}}
        with patch.object(module, "events", return_value=[changed]), \
             patch.object(module, "aid", return_value="agt_a"):
            with self.assertRaisesRegex(AssertionError, "full saved ask"):
                module.ask_history("approval", "once", "1,0,0", True)

    def test_ask_history_cli_dispatch_snapshot_compare_and_bad_mode(self):
        original = {"event_id": "ask.opened::ses:per_1", "seq": 1,
                    "kind": "ask.opened", "turn_id": "turn_1",
                    "payload": {"askId": "per_1", "text": "owned command"}}
        prefix = ["coverage-chat-controls.py", "ask-history", "approval", "once", "1,0,0"]
        with patch.object(module, "events", return_value=[original]), \
             patch.object(module, "aid", return_value="agt_owned"), \
             patch.object(sys, "argv", prefix):
            module.main()
        self.assertEqual(module.saved("approval-once-ask-history.json"), {
            "agent_id": "agt_owned", "events": [original], "expected_counts": [1, 0, 0]})
        with patch.object(module, "events", return_value=[original]), \
             patch.object(module, "aid", return_value="agt_owned"), \
             patch.object(sys, "argv", [*prefix, "compare"]):
            module.main()
        changed = {**original, "payload": {"askId": "per_1", "text": "changed command"}}
        with patch.object(module, "events", return_value=[changed]), \
             patch.object(module, "aid", return_value="agt_owned"), \
             patch.object(sys, "argv", [*prefix, "compare"]):
            with self.assertRaisesRegex(AssertionError, "full saved ask"):
                module.main()
        with patch.object(sys, "argv", [*prefix, "skip"]):
            with self.assertRaisesRegex(AssertionError, "ask-history usage"):
                module.main()

    def test_modal_refuses_default_outside_wired_harnesses(self):
        for result in ({"options": ["opencode"], "selected": "codex"},
                       {"options": ["opencode", "codex"], "selected": "opencode"}):
            with self.subTest(result=result), \
                 patch.object(module, "call", return_value={"harnesses": ["opencode"]}), \
                 patch.object(module, "browser", side_effect=["", json.dumps(result)]):
                with self.assertRaisesRegex(AssertionError, "differ from wired"):
                    module.modal_backends()
                self.assertEqual(module.saved("modal-wired-backends.json")["options"], result["options"])

    def test_ask_reload_refuses_identical_events_from_foreign_agent(self):
        event = {"event_id": "ask.opened::ses:per_1", "seq": 1,
                 "kind": "ask.opened", "turn_id": "turn_1", "payload": {"askId": "per_1"}}
        module.save("approval-once-ask-history.json", {
            "agent_id": "agt_foreign", "expected_counts": [1, 0, 0], "events": [event]})
        with patch.object(module, "events", return_value=[event]), \
             patch.object(module, "aid", return_value="agt_owned"):
            with self.assertRaisesRegex(AssertionError, "another agent"):
                module.ask_history("approval", "once", "1,0,0", True)


if __name__ == "__main__":
    unittest.main()
