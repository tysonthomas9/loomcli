#!/usr/bin/env python3
"""Offline negative checks for the live suite's evidence oracles."""

import importlib.util
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

for key, value in {
    "AFT_WORK_DIR": "/tmp/coverage-chat-controls-oracle",
    "RUN_ID": "oracle",
    "AFT_WS": "LOCALMODE",
    "AFT_API_URL": "http://127.0.0.1:1",
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

    def test_allow_requires_native_command_effect(self):
        with patch.object(module, "events", return_value=[{"kind": "ask.opened", "event_id": "e1", "payload": {}}]):
            with self.assertRaisesRegex(AssertionError, "executed commands"):
                module.approval_effect("approval", "MARKER", "1", "1")


if __name__ == "__main__":
    unittest.main()
