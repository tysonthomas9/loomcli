#!/usr/bin/env python3
"""Offline checks for the scoped privacy oracle, never counted as live proof."""

import json
import os
from pathlib import Path
import runpy
import subprocess
import unittest
from unittest.mock import patch

os.environ.setdefault("RUN_ID", "offline")
os.environ.setdefault("AFT_WS", "LOCALMODE")
os.environ.setdefault("AFT_API_URL", "http://127.0.0.1:1")
os.environ.setdefault("AFT_WORK_DIR", "/private/tmp/coverage-tool-policy-offline")
policy = runpy.run_path(str(Path(__file__).with_name("coverage-tool-policy.py")))
assert_private_nodes = policy["assert_private_nodes"]
assert_expanded_state = policy["assert_expanded_state"]
assert_native_steps = policy["assert_native_steps"]
event_item_id = policy["event_item_id"]
assert_reviewer_binding = policy["assert_reviewer_binding"]
current_child_tool_events = policy["current_child_tool_events"]
tool_events = policy["tool_events"]
scoped_dom = policy["SCOPED_DOM"]
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
    def test_saved_sentinel_receipt_allows_extra_benign_tool(self):
        sentinel_id = "msg_1/tool/call_sensitive"
        sensitive = {"kind": "item.completed", "event_id": "item.completed:root:native:" + sentinel_id,
                     "payload": {"itemKind": "tool", "tool": {
                         "input": "{\"command\":\"printf SAFE # TOKEN=REDACTED\"}",
                         "output": "SAFE", "failed": False}}}
        benign = {"kind": "item.completed", "event_id": "item.completed:root:native:msg_2/tool/call_read",
                  "payload": {"itemKind": "tool", "tool": {"input": "{\"filePath\":\"README.md\"}"}}}
        globals_ = tool_events.__globals__
        original = globals_["events"], globals_["native_tool_ids"]
        globals_["events"] = lambda kind: [benign, sensitive]
        globals_["native_tool_ids"] = lambda kind: [sentinel_id]
        try:
            self.assertEqual(tool_events()[1], [sensitive])
            raw = {**sensitive, "payload": {"itemKind": "tool", "tool": {
                "input": "printf SAFE # TOKEN=" + policy["SENTINEL"], "output": "SAFE"}}}
            globals_["events"] = lambda kind: [benign, raw]
            with self.assertRaisesRegex(AssertionError, "redaction receipt"):
                tool_events()
        finally:
            globals_["events"], globals_["native_tool_ids"] = original

    def test_dom_probe_reads_nested_tool_control(self):
        for value in ("false", "true"):
            script = """
              const value=process.argv[1];
              const button={getAttribute:k=>k==='aria-expanded'?value:null};
              const root={textContent:'Ran command',
                getAttribute:k=>k==='data-testid'?'tool-call':null,
                querySelectorAll:()=>[],
                querySelector:s=>s==='[role=button][aria-expanded]'?button:null};
              global.document={querySelectorAll:()=>[root]};
              process.stdout.write(JSON.stringify(PROBE));
            """.replace("PROBE", scoped_dom)
            nodes = json.loads(subprocess.check_output(
                ["node", "-e", script, value], text=True))
            self.assertEqual(nodes[0]["expanded"], value)

    def test_expanded_card_must_be_observed_on_nested_control(self):
        closed = {"kind": "tool-call", "text": "Ran command", "aria": "", "expanded": "false"}
        opened = {**closed, "expanded": "true"}
        assert_expanded_state([closed], "collapsed")
        assert_expanded_state([opened], "expanded")
        assert_expanded_state([opened], "reloaded")
        with self.assertRaisesRegex(AssertionError, "one or more native tool cards"):
            assert_expanded_state([closed], "expanded")
        assert_expanded_state([opened, opened], "expanded")  # an extra benign call is allowed
        with self.assertRaisesRegex(AssertionError, "one or more native tool cards"):
            assert_expanded_state([opened, closed], "expanded")  # wrong card opened
        with self.assertRaisesRegex(AssertionError, "already expanded"):
            assert_expanded_state([opened], "collapsed")

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
        saved = [{"kind": "usage", "event_id": "usage:root:native:a",
                  "payload": {"itemId": "REDACTED", "inputTokens": 3}},
                 {"kind": "usage", "event_id": "usage:root:native:b",
                  "payload": {"itemId": "REDACTED", "inputTokens": 7}}]
        native = [{"itemID": "a", "inputTokens": 7},
                  {"itemID": "b", "inputTokens": 3}]
        for step in native:
            step.update(outputTokens=0, cacheReadTokens=0, cacheWriteTokens=0, costUsd=0)
        self.assertEqual(sum(e["payload"]["inputTokens"] for e in saved),
                         sum(e["inputTokens"] for e in native))
        with self.assertRaisesRegex(AssertionError, "native step"):
            assert_native_steps(saved, native)

    def test_saved_event_identity_survives_payload_redaction(self):
        event = {"kind": "usage", "event_id": "usage:root:native:msg_12",
                 "payload": {"itemId": "REDACTED"}}
        self.assertEqual(event_item_id(event), "msg_12")


class OwnedEvidenceOracle(unittest.TestCase):
    def test_rejects_foreign_reviewer_id_or_checkout(self):
        repo = "/root/.loom/workspaces/LOCALMODE/source-repo"
        row = {"agent_id": "agt_owned", "worktree_path": "/root/.loom/worktrees/source-repo/agt_owned",
               "repo": repo, "preset": "pr-review-interactive", "harness": "opencode",
               "created_by_kind": "user", "parent_agent_id": None}
        state = {"agent_id": "agt_owned", "checkout": row["worktree_path"], "repo": repo,
                 "preset": row["preset"], "harness": "opencode"}
        assert_reviewer_binding(row, state, repo)
        with self.assertRaisesRegex(AssertionError, "exact saved reviewer Agent ID"):
            assert_reviewer_binding(row, {**state, "agent_id": "agt_foreign"}, repo)
        with self.assertRaisesRegex(AssertionError, "foreign checkout"):
            assert_reviewer_binding({**row, "worktree_path": "/workspace/foreign"}, state, repo)

    def test_rejects_old_or_foreign_child_tool_event(self):
        sentinel = "ghp_AFTONLYofflineZZZZZZZZZZZZZZZZ"
        tool = {"agent_id": "agt_child", "turn_id": "turn_now", "seq": 2,
                "event_id": "tool:2", "kind": "item.completed",
                "payload": {"itemKind": "tool", "tool": {"input": "printf SAFE # TOKEN=REDACTED"}}}
        self.assertEqual(current_child_tool_events([tool], "agt_child", "turn_now", sentinel), [tool])
        for stale in ({**tool, "turn_id": "turn_old"}, {**tool, "agent_id": "agt_foreign"}):
            with self.assertRaisesRegex(AssertionError, "no saved native child tool call"):
                current_child_tool_events([stale], "agt_child", "turn_now", sentinel)
        reasoning = {"agent_id": "agt_child", "turn_id": "turn_now", "seq": 3,
                     "event_id": "reason:3", "kind": "item.completed",
                     "payload": {"itemKind": "reasoning"}}
        with self.assertRaisesRegex(AssertionError, "not the child's latest saved step"):
            current_child_tool_events([tool, reasoning], "agt_child", "turn_now", sentinel)
        other = {**tool, "seq": 3, "event_id": "tool:3",
                 "payload": {"itemKind": "tool", "tool": {"input": "printf SAFE # TOKEN=" + sentinel}}}
        with self.assertRaisesRegex(AssertionError, "did not redact"):
            current_child_tool_events([tool, other], "agt_child", "turn_now", sentinel)


if __name__ == "__main__":
    unittest.main()
