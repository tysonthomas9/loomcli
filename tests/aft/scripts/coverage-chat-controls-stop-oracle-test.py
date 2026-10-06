#!/usr/bin/env python3
"""Offline refusal checks for the bare native Stop evidence oracle."""

import importlib.util
import os
import unittest
from pathlib import Path
from unittest.mock import patch

for key, value in {"AFT_WORK_DIR": "/tmp/coverage-chat-controls-stop-oracle",
                   "RUN_ID": "oracle", "AFT_WS": "LOCALMODE",
                   "AFT_API_URL": "http://127.0.0.1:1",
                   "AFT_REAL_MODEL": "openai/gpt"}.items():
    os.environ.setdefault(key, value)

spec = importlib.util.spec_from_file_location(
    "coverage_chat_controls_stop", Path(__file__).with_name("coverage-chat-controls-stop.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class StopOracleTest(unittest.TestCase):
    def setUp(self):
        self.identity = {"ask_id": "per_1", "turn_id": "turn_1"}
        self.end = {"kind": "agent.turn_completed", "seq": 4, "turn_id": "turn_1",
                    "event_id": "agent.turn_completed::ses:turn_1", "payload": {"stopReason": "cancelled"}}
        self.lost = {"kind": "ask.lost", "seq": 5, "turn_id": "turn_1",
                     "event_id": "ask.lost::ses:per_1", "payload": {"askId": "per_1"}}

    def check(self, rows):
        with patch.object(module.controls, "saved", return_value=self.identity), \
             patch.object(module.controls, "agent", return_value={"running_turn_id": None, "open_asks": []}), \
             patch.object(module.controls, "events", return_value=rows), \
             patch.object(module.controls, "aid", return_value="agt_a"), \
             patch.object(module.controls, "browser", return_value="[]"), \
             patch.object(module.controls, "ask_history"), \
             patch.object(module.controls, "save"):
            module.stopped()

    def test_stop_refuses_resolved_ask_instead_of_lost_ask(self):
        resolved = {"kind": "ask.resolved", "seq": 5, "turn_id": "turn_1",
                    "payload": {"askId": "per_1"}}
        with self.assertRaisesRegex(AssertionError, "lose the exact ask"):
            self.check([self.end, resolved])

    def test_stop_refuses_a_proposed_tool_effect(self):
        effect = {"kind": "item.completed", "seq": 3, "turn_id": "turn_1",
                  "payload": {"itemKind": "tool", "tool": {"output": "STOP_EFFECT_oracle"}}}
        with self.assertRaisesRegex(AssertionError, "effect executed"):
            self.check([effect, self.end, self.lost])


if __name__ == "__main__":
    unittest.main()
