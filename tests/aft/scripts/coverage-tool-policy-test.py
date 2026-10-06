#!/usr/bin/env python3
"""Offline checks for the scoped privacy oracle, never counted as live proof."""

import os
from pathlib import Path
import runpy
import unittest
from unittest.mock import patch

os.environ.setdefault("RUN_ID", "offline")
os.environ.setdefault("AFT_WS", "LOCALMODE")
os.environ.setdefault("AFT_API_URL", "http://127.0.0.1:1")
os.environ.setdefault("AFT_WORK_DIR", "/private/tmp/coverage-tool-policy-offline")
policy = runpy.run_path(str(Path(__file__).with_name("coverage-tool-policy.py")))
assert_private_nodes = policy["assert_private_nodes"]
assert_native_steps = policy["assert_native_steps"]
api = policy["api"]


class APIResponseOracle(unittest.TestCase):
    def test_archive_accepts_exact_empty_204(self):
        class EmptyNoContent:
            status = 204

            def __enter__(self):
                return self

            def __exit__(self, *_):
                return False

            def read(self):
                return b""

        with patch("urllib.request.urlopen", return_value=EmptyNoContent()):
            self.assertIsNone(api("/owned-agent/archive", "POST", {"reason": "cancelled"}))

    def test_archive_rejects_unexpected_body_on_204(self):
        class BadNoContent:
            status = 204

            def __enter__(self):
                return self

            def __exit__(self, *_):
                return False

            def read(self):
                return b"unexpected"

        with patch("urllib.request.urlopen", return_value=BadNoContent()):
            with self.assertRaisesRegex(AssertionError, "unexpectedly had a body"):
                api("/owned-agent/archive", "POST", {"reason": "cancelled"})


class ScopedPrivacyOracle(unittest.TestCase):
    def test_user_authored_text_is_outside_tool_scope(self):
        sentinel = "ghp_AFTONLYofflineZZZZZZZZZZZZZZZZ"
        user_bubble = {"kind": "user", "text": sentinel, "aria": ""}
        tool = {"kind": "tool-call", "text": "Ran command · SAFE", "aria": "Ran command"}
        self.assertIn(sentinel, user_bubble["text"])
        assert_private_nodes([tool], sentinel)

    def test_raw_plain_or_json_tool_input_fails_even_when_collapsed_is_safe(self):
        sentinel = "ghp_AFTONLYofflineZZZZZZZZZZZZZZZZ"
        collapsed = {"kind": "tool-call", "text": "Ran command", "aria": "Ran command"}
        for raw in (f"Authorization: Bearer {sentinel}",
                    '{"command":"printf SAFE # Bearer ' + sentinel + '"}'):
            expanded = {"kind": "tool-call", "text": "Input\n" + raw, "aria": "Ran command"}
            assert_private_nodes([collapsed], sentinel)
            with self.assertRaisesRegex(AssertionError, "leaked"):
                assert_private_nodes([expanded], sentinel)

    def test_accessibility_label_also_fails(self):
        sentinel = "ghp_AFTONLYofflineZZZZZZZZZZZZZZZZ"
        for kind in ("tool-live", "agent-tray"):
            with self.assertRaisesRegex(AssertionError, "leaked"):
                assert_private_nodes([{"kind": kind, "text": "Running", "aria": sentinel}], sentinel)


class NativeUsageOracle(unittest.TestCase):
    def test_rejects_wrong_step_even_when_totals_match(self):
        saved = [{"payload": {"itemID": "a", "inputTokens": 3}},
                 {"payload": {"itemID": "b", "inputTokens": 7}}]
        native = [{"itemID": "a", "inputTokens": 7},
                  {"itemID": "b", "inputTokens": 3}]
        for step in native:
            step.update(outputTokens=0, cacheReadTokens=0, cacheWriteTokens=0, costUsd=0)
        self.assertEqual(sum(e["payload"]["inputTokens"] for e in saved),
                         sum(e["inputTokens"] for e in native))
        with self.assertRaisesRegex(AssertionError, "native step"):
            assert_native_steps(saved, native)


if __name__ == "__main__":
    unittest.main()
