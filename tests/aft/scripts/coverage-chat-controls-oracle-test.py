#!/usr/bin/env python3
"""Offline negative checks for the live suite's evidence oracles."""

import importlib.util
import os
import subprocess
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


if __name__ == "__main__":
    unittest.main()
