#!/usr/bin/env python3
"""Offline regressions for event-backed child completion predicates."""

import importlib.util
import inspect
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import tempfile
import unittest
from unittest.mock import patch


os.environ.setdefault("RUN_ID", "af12345678")
os.environ.setdefault("AFT_WS", "LOCALMODE")
os.environ.setdefault("AFT_API_URL", "http://127.0.0.1:1")
os.environ.setdefault("AFT_WORK_DIR", tempfile.mkdtemp(prefix="coverage-child-predicates-"))
os.environ.setdefault("AFT_AGENT_FLOW_REPO", "source-repo")
os.environ.setdefault("AFT_REAL_BACKEND", "opencode")
spec = importlib.util.spec_from_file_location("child_oracles", Path(__file__).with_name("coverage-children-activity.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def parsed_runs():
    tests = Path(__file__).resolve().parents[1]
    suite_path = tests / "live-agent-coverage-suites/children-activity.test.yaml"
    loader = Path(os.environ.get("AFT_DIR", "/Users/tyson/codebase/code-agents/testing-app")) / "dist/runner.js"
    assert loader.is_file(), f"AFT parsed-suite loader missing: {loader}"
    code = """import {pathToFileURL} from 'node:url';
const [loader,file]=process.argv.slice(2);
const {loadSuite}=await import(pathToFileURL(loader).href);
const suite=loadSuite(file);
console.log(JSON.stringify([suite.teardown,...suite.tests.flatMap(t=>t.steps.filter(s=>s.run).map(s=>s.run))]));"""
    env = {**os.environ, "AFT_BASE_URL": "http://127.0.0.1:1", "AFT_REAL_MODEL": "offline",
           "AFT_NATIVE_MODEL_PROBE": "/bin/true", "AFT_SELECT_AGENT_MODEL": "/bin/true"}
    output = subprocess.check_output(["node", "--input-type=module", "-", str(loader), str(suite_path)],
                                     input=code, env=env, text=True)
    return json.loads(output)


def parsed_pair_busy_prompt():
    tests = Path(__file__).resolve().parents[1]
    loader = Path(os.environ.get("AFT_DIR", "/Users/tyson/codebase/code-agents/testing-app")) / "dist/runner.js"
    code = """import {pathToFileURL} from 'node:url';
const [loader,file]=process.argv.slice(2);
const {loadSuite}=await import(pathToFileURL(loader).href);
const suite=loadSuite(file);
const steps=suite.tests.find(t=>t.name.startsWith('two task children finish')).steps;
console.log(JSON.stringify(steps.filter(s=>s.fill?.label==='Message').map(s=>s.fill.value)));"""
    env = {**os.environ, "AFT_BASE_URL": "http://127.0.0.1:1", "AFT_REAL_MODEL": "offline",
           "AFT_NATIVE_MODEL_PROBE": "/bin/true", "AFT_SELECT_AGENT_MODEL": "/bin/true"}
    output = subprocess.check_output(["node", "--input-type=module", "-", str(loader),
                                      str(tests / "live-agent-coverage-suites/children-activity.test.yaml")],
                                     input=code, env=env, text=True)
    return next(prompt for prompt in json.loads(output) if "valid post" in prompt)


def parsed_commands():
    commands = []
    for run in parsed_runs():
        if not isinstance(run, str):
            continue
        for line in run.splitlines():
            match = re.search(r'python3 "\$AFT_TESTS_DIR/scripts/coverage-children-activity\.py"\s+(.+)', line)
            if match:
                args = match[1].strip().removesuffix(')"')
                commands.append(shlex.split(args))
    return commands


def event(kind, seq, event_id, payload=None, turn_id=""):
    return {"kind": kind, "seq": seq, "event_id": event_id, "turn_id": turn_id, "payload": payload or {}}


class ChildProofPredicates(unittest.TestCase):
    def setUp(self):
        self.a, self.b = "agt_pairA", "agt_pairB"
        self.saved = {}
        self.records = [event("task_completed", 11, f"task_completed:{self.a}:0", {"outcome": "completed", "head": "abc"}),
                        event("task_completed", 12, f"task_completed:{self.b}:0", {"outcome": "completed", "head": "abc"})]
        self.delivery = event("message.delivered", 21, "delivered", {"completions": [
            {"child": self.a, "attempt": 0}, {"child": self.b, "attempt": 0}]})
        self.history = self.records + [event("agent.turn_completed", 20, "busy-end", turn_id="turn-busy"),
                                       self.delivery,
                                       event("item.completed", 22, "reply", {"itemKind": "message", "text": "pair-a pair-b done"}),
                                       event("agent.turn_completed", 23, "reply-end", turn_id="turn-final")]
        self.labels = {"pair-lead": {"agent_id": "agt_lead"},
                       "busy": {"children": [self.a, self.b], "lead_turn": "turn-busy", "last_lead_seq": 10},
                       "pair-a": {"agent_id": self.a, "name": "pair-a"},
                       "pair-b": {"agent_id": self.b, "name": "pair-b"}}

    def test_parsed_yaml_cli_arity_and_child_bind_contract(self):
        commands = parsed_commands()
        self.assertGreaterEqual(len(commands), 40, "parsed suite lost helper invocations")
        for command, *args in commands:
            with self.subTest(command=command, args=args):
                inspect.signature(getattr(module, command)).bind(*args)
        binds = [args for command, *args in commands if command == "bind"]
        self.assertEqual(len(binds), 8)
        children = [args for args in binds if len(args) == 3]
        self.assertEqual(len(children), 4)
        expected = {"repeat": "repeat-lead", "pair-a": "pair-lead", "pair-b": "pair-lead",
                    "sidebar-task": "sidebar-b"}
        with tempfile.TemporaryDirectory(prefix="parsed-child-bind-") as temp, patch.object(module, "OUT", Path(temp)):
            for label, name_template, parent_label in children:
                with self.subTest(label=label):
                    name = name_template.replace("${RUN_ID}", module.RUN)
                    self.assertEqual(parent_label, expected[label])
                    self.assertEqual(name, f"cov-child-{label}-{module.RUN}")
                    parent_id, child_id = f"agt_parent_{label}", f"agt_child_{label}"
                    parent = {"agent_id": parent_id, "name": f"cov-child-{parent_label}-{module.RUN}",
                              "branch": "main", "worktree_path": f"/owned/{parent_label}"}
                    prior = {"head": "a" * 40, "branch": "main", "worktree": parent["worktree_path"]}
                    child = {"agent_id": child_id, "name": name, "repo": os.environ["AFT_AGENT_FLOW_REPO"],
                             "harness": os.environ["AFT_REAL_BACKEND"], "preset": "task", "created_by_kind": "agent",
                             "created_by_id": parent_id, "parent_agent_id": parent_id, "root_agent_id": parent_id,
                             "worktree_path": f"/owned/{label}", "base_ref": "main", "branch": f"branch-{label}"}
                    ref = {"agent_id": child_id, "branch": child["branch"], "head": "b" * 40,
                           "merge_base": prior["head"]}
                    created = event("child.created", 1, "created", {"child": child_id})
                    tool = event("item.completed", 2, "tool", {"itemKind": "tool", "tool": {
                        "name": "loom.agent_create", "input": json.dumps({"name": name})}})
                    module.save(parent_label, parent)
                    module.save(parent_label + "-precreate-ref", prior)
                    with patch.object(module, "listed", side_effect=lambda pid: [{"agent_id": child_id, "name": name}] if pid == parent_id else []), \
                         patch.object(module, "agent", return_value=child), \
                         patch.object(module, "read_ref", return_value=ref), \
                         patch.object(module, "events", return_value=[created, tool]), \
                         patch("builtins.print"):
                        module.bind(label, name, parent_label)
                        self.assertEqual(module.load(label)["agent_id"], child_id)
                        with self.assertRaises((FileNotFoundError, AssertionError)):
                            module.bind(label, parent_label, name)

    def run_pair(self):
        with patch.object(module, "load", side_effect=lambda label: self.labels[label]), \
             patch.object(module, "events", side_effect=lambda agent_id: self.history if agent_id == "agt_lead" else [
                 event("item.completed", 2, "answer", {"itemKind": "message", "text": "done"}),
                 event("agent.turn_completed", 3, "finished")]), \
             patch.object(module, "agent", return_value={"base_ref": "main"}), \
             patch.object(module, "get", side_effect=lambda url: {"data": {
                 "files": [{"path": f"aft-child-fixtures/{module.RUN}/pair-{'a' if self.a in url else 'b'}.txt"}]}}
                 if "/files?" in url else {"data": {"commits": [{"hash": "abc"}]}}), \
             patch.object(module, "save", side_effect=lambda name, data: self.saved.update({name: data})):
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
        self.assertEqual(self.saved["pair-chronology"]["busy_end"][0]["seq"], 20)
        self.assertEqual([r["events"][0]["seq"] for r in self.saved["pair-chronology"]["child_records"]], [11, 21])

    def test_duplicate_delivery_is_rejected(self):
        self.history.append(event("message.delivered", 23, "duplicate", {"completions": [
            {"child": self.a, "attempt": 0}]}))
        with self.assertRaises(AssertionError):
            self.run_pair()

    def test_pair_rejects_streaming_reply_without_saved_final_turn_end(self):
        self.history = [e for e in self.history if e["event_id"] != "reply-end"]
        with self.assertRaisesRegex(AssertionError, "persisted finished turn"):
            self.run_pair()

    def test_reactivation_requires_captured_running_tray_and_nested_sidebar(self):
        child = {"agent_id": self.a, "parent_agent_id": "agt_lead", "name": "cov-child-repeat-" + module.RUN}
        captured = {"path": "/ws/LOCALMODE/chat/agt_lead", "rowId": self.a, "linkId": self.a,
                    "rowVisible": True, "running": True, "nested": True, "exactLinkCount": 1, "childName": child["name"],
                    "attemptChip": "attempt 2", "logo": True, "dot": "working", "color": "3",
                    "result": "▸ Ran code · inspect", "elapsed": "0m 02s", "capturedAt": 123}
        self.assertEqual(module.reactivation_checkpoint_ok({"capture": captured, "cardAttempts": ["0"]}, child), captured)
        for bad in ({"capture": None, "cardAttempts": ["0", "1"]},
                    {"capture": {**captured, "path": "/ws/FOREIGN/chat/agt_lead"}},
                    {"capture": {**captured, "rowId": self.b}},
                    {"capture": {**captured, "linkId": self.b}},
                    {"capture": {**captured, "attemptChip": "attempt 1"}},
                    {"capture": {**captured, "running": False}},
                    {"capture": {**captured, "nested": False}},
                    {"capture": {**captured, "result": "Working…"}},
                    {"capture": {**captured, "dot": "done"}}):
            with self.subTest(bad=bad), self.assertRaises(AssertionError):
                module.reactivation_checkpoint_ok(bad, child)

    def test_pre_send_observer_uses_exact_child_parent_and_valid_js(self):
        child = {"agent_id": self.a, "parent_agent_id": "agt_lead", "name": "cov-child-repeat-" + module.RUN}
        lead = {"agent_id": "agt_lead", "name": "cov-child-repeat-lead-" + module.RUN}
        scripts = []
        history = [event("turn.started", 1, "first-turn", turn_id="turn-one")]
        with patch.object(module, "load", side_effect=lambda label: lead if label == "repeat-lead" else
                          {"child": self.a, "attempt": 0} if label == "first" else child), \
             patch.object(module, "agent", return_value={**child, "attempt": 0, "state": "finished"}), \
             patch.object(module, "events", return_value=history), \
             patch.object(module, "browser_json", side_effect=lambda script: (scripts.append(script) or
                          {"armed": True, "id": self.a, "path": "/ws/LOCALMODE/chat/agt_lead", "capturedBeforeSend": False})), \
             patch.object(module, "save"):
            module.arm_reactivation("repeat-lead", "repeat")
        self.assertIn(json.dumps(self.a), scripts[0])
        self.assertIn(json.dumps(lead["name"]), scripts[0])
        self.assertIn("new MutationObserver(sample)", scripts[0])
        check = subprocess.run(["node", "--input-type=module", "-e", "new Function('return '+process.argv[1])", scripts[0]],
                               capture_output=True, text=True)
        self.assertEqual(check.returncode, 0, check.stderr)

    def test_reactivation_event_rejects_prior_turn_wrong_child_and_wrong_step(self):
        started = event("turn.started", 11, "second-start", turn_id="turn-two")
        step = event("item.completed", 12, "second-step", {"itemKind": "tool", "tool": {"name": "execute"}}, "turn-two")
        started["agent_id"] = step["agent_id"] = self.a
        self.assertEqual(module.reactivation_event([started, step], self.a, 10, "▸ Ran code · inspect")["event_id"], "second-step")
        for rows, result in (([started, step], "▸ Read file"),
                             ([{**started, "seq": 9}, step], "▸ Ran code"),
                             ([started, {**step, "agent_id": self.b}], "▸ Ran code"),
                             ([started, {**step, "turn_id": "turn-old"}], "▸ Ran code")):
            with self.subTest(rows=rows, result=result), self.assertRaises(AssertionError):
                module.reactivation_event(rows, self.a, 10, result)

    def test_reactivation_event_uses_product_reasoning_first_line(self):
        started = event("turn.started", 11, "second-start", turn_id="turn-two")
        started["agent_id"] = self.a
        for raw, shown in (("**Planning file creation and commits**", "Planning file creation and commits"),
                           ("## Planning file creation and commits", "Planning file creation and commits"),
                           ("`Planning file creation and commits`", "Planning file creation and commits"),
                           ("Planning file creation and commits", "Planning file creation and commits"),
                           ("**Planning** _file_ `creation`", "Planning file creation")):
            step = event("item.completed", 12, "reason", {"itemKind": "reasoning", "itemId": "reason-2",
                         "text": raw + "\nprivate later line"}, "turn-two")
            step["agent_id"] = self.a
            with self.subTest(raw=raw):
                self.assertEqual(module.reactivation_event([started, step], self.a, 10,
                                 f"💭 Thinking · {shown} · 0:01")["event_id"], "reason")
                with self.assertRaises(AssertionError):
                    module.reactivation_event([started, step], self.a, 10, "💭 Thinking · unrelated · 0:01")
                for altered in ({**step, "agent_id": self.b}, {**step, "turn_id": "turn-old"},
                                {**step, "seq": 10}):
                    with self.assertRaises(AssertionError):
                        module.reactivation_event([started, altered], self.a, 10,
                                                  f"💭 Thinking · {shown} · 0:01")

    def test_reactivation_failure_saves_bounded_identity_diagnostic_without_text(self):
        child = {"agent_id": self.a, "parent_agent_id": "agt_lead", "name": "cov-child-repeat-" + module.RUN}
        capture = {"path": "/ws/LOCALMODE/chat/agt_lead", "rowId": self.a, "linkId": self.a,
                   "rowVisible": True, "running": True, "nested": True, "exactLinkCount": 1,
                   "childName": child["name"], "attemptChip": "attempt 2", "logo": True,
                   "dot": "working", "color": "3", "result": "💭 Thinking · unmatched · 0:01",
                   "elapsed": "0m 02s", "capturedAt": 123}
        started = event("turn.started", 11, "second-start", turn_id="turn-two")
        step = event("item.completed", 12, "reason", {"itemKind": "reasoning", "itemId": "reason-2",
                     "text": "**secret reasoning title**\nprivate later line"}, "turn-two")
        started["agent_id"] = step["agent_id"] = self.a
        saved = {}
        with patch.object(module, "load", side_effect=lambda label: {"agent_id": "agt_lead"} if label == "repeat-lead" else
                          {"child": self.a, "parent": "agt_lead", "last_seq": 10} if label == "reactivation-armed" else
                          {"value": "3"} if label == f"color-{self.a}" else child), \
             patch.object(module, "agent", return_value={**child, "attempt": 1, "state": "active",
                                                        "running_turn_id": "turn-two"}), \
             patch.object(module, "events", return_value=[started, step]), \
             patch.object(module, "browser"), \
             patch.object(module, "browser_json", return_value={"capture": capture, "cardAttempts": ["0"]}), \
             patch.object(module, "save", side_effect=lambda name, data: saved.update({name: data})), \
             self.assertRaises(AssertionError):
            module.reactivation_checkpoint("repeat-lead", "repeat")
        diagnostic = saved["reactivation-event-diagnostic"]
        self.assertEqual(diagnostic["child_id"], self.a)
        self.assertEqual(diagnostic["api_running_turn_id"], "turn-two")
        self.assertEqual(diagnostic["captured_at_ms"], 123)
        self.assertEqual(diagnostic["events"][-1]["item_id"], "reason-2")
        self.assertIn("strong", diagnostic["events"][-1]["formatting"])
        self.assertNotIn("secret reasoning title", json.dumps(diagnostic))
        self.assertNotIn("private later line", json.dumps(diagnostic))

    def test_reactivation_diagnostic_caps_events_and_never_saves_tool_io(self):
        history = []
        for seq in range(11, 61):
            row = event("item.completed", seq, f"item:{seq}",
                        {"itemKind": "tool", "itemId": f"item-{seq}",
                         "tool": {"name": "execute", "input": "private tool input",
                                  "output": "private tool output"}}, "turn-two")
            row["agent_id"] = self.a
            history.append(row)
        capture = {"capturedAt": 123, "result": "▸ Ran code · 0:01"}
        live = {"state": "active", "attempt": 1, "running_turn_id": "turn-two"}
        diagnostic = module.reactivation_event_diagnostic(history, self.a, 10, capture, live)
        self.assertEqual((diagnostic["candidate_count"], diagnostic["omitted_count"], len(diagnostic["events"])),
                         (50, 10, 40))
        self.assertEqual(diagnostic["events"][0]["seq"], 21)
        self.assertNotIn("private tool", json.dumps(diagnostic))

    def test_product_reasoning_limit_counts_utf16_units(self):
        raw = "A" * 110
        rendered = module.step_prefix(event("item.completed", 12, "reason",
                             {"itemKind": "reasoning", "text": raw}, "turn-two"))
        self.assertEqual(len(rendered.encode("utf-16-le")) // 2, 60)
        self.assertTrue(rendered.endswith("…"))

    def test_reactivation_opens_real_collapsed_tray_only_during_saved_second_turn(self):
        lead = {"agent_id": "agt_lead", "name": "lead"}
        child = {"agent_id": self.a, "parent_agent_id": "agt_lead", "name": "repeat"}
        baseline = {"child": self.a, "parent": "agt_lead", "last_seq": 10}
        started = event("turn.started", 11, "second-start", turn_id="turn-two")
        started["agent_id"] = self.a
        before = {"path": "/ws/LOCALMODE/chat/agt_lead", "open": "false", "expanded": "false",
                  "header": "1 agent: 1 running", "observerArmed": True, "capturedBeforeClick": False}
        after = {"path": before["path"], "open": "true", "expanded": "true", "captured": True}
        calls, saved, scripts = [], {}, []
        def run(before_value=before, live_state="active", history=None):
            values = iter((before_value, after))
            with patch.object(module, "load", side_effect=lambda label: lead if label == "repeat-lead" else
                              baseline if label == "reactivation-armed" else child), \
                 patch.object(module, "agent", return_value={**child, "state": live_state, "attempt": 1,
                                                              "running_turn_id": "turn-two"}), \
                 patch.object(module, "events", return_value=[started] if history is None else history), \
                 patch.object(module, "browser_json", side_effect=lambda script: (scripts.append(script) or next(values))), \
                 patch.object(module, "browser", side_effect=lambda *args: calls.append(args)), \
                 patch.object(module, "save", side_effect=lambda name, value: saved.update({name: value})):
                module.open_reactivation_tray("repeat-lead", "repeat")
        run()
        self.assertEqual(calls[0], ("click", "[data-testid=agent-tray] button[aria-expanded=false]"))
        self.assertEqual(calls[1][0], "screenshot")
        for script in scripts[:2]:
            check = subprocess.run(["node", "--input-type=module", "-e", "new Function('return '+process.argv[1])", script],
                                   capture_output=True, text=True)
            self.assertEqual(check.returncode, 0, check.stderr)
        self.assertEqual(saved["reactivation-tray-before-click"]["started_event_ids"], ["second-start"])
        for bad in ({**before, "open": "true"}, {**before, "header": "1 agent: 1 done"},
                    {**before, "observerArmed": False}, {**before, "path": "/ws/FOREIGN/chat/agt_lead"}):
            with self.subTest(bad=bad), self.assertRaises(AssertionError):
                run(before_value=bad)
        with self.assertRaisesRegex(AssertionError, "exact child attempt-two turn"):
            run(history=[])

    def test_reactivation_wait_compiles_and_saved_step_binds_capture(self):
        child = {"agent_id": self.a, "parent_agent_id": "agt_lead", "name": "cov-child-repeat-" + module.RUN}
        capture = {"path": "/ws/LOCALMODE/chat/agt_lead", "rowId": self.a, "linkId": self.a,
                   "rowVisible": True, "running": True, "nested": True, "exactLinkCount": 1, "childName": child["name"],
                   "attemptChip": "attempt 2", "logo": True, "dot": "working", "color": "3",
                   "result": "▸ Ran code · inspect", "elapsed": "0m 02s", "capturedAt": 123}
        started = event("turn.started", 11, "second-start", turn_id="turn-two")
        step = event("item.completed", 12, "second-step", {"itemKind": "tool", "tool": {"name": "execute"}}, "turn-two")
        started["agent_id"] = step["agent_id"] = self.a
        calls, saved, scripts = [], {}, []
        with patch.object(module, "load", side_effect=lambda label: {"agent_id": "agt_lead"} if label == "repeat-lead" else
                          {"child": self.a, "parent": "agt_lead", "last_seq": 10} if label == "reactivation-armed" else
                          {"value": "3"} if label == f"color-{self.a}" else child), \
             patch.object(module, "agent", return_value={**child, "attempt": 1, "state": "active"}), \
             patch.object(module, "events", return_value=[started, step]), \
             patch.object(module, "browser", side_effect=lambda *args: calls.append(args)), \
             patch.object(module, "browser_json", side_effect=lambda script: (scripts.append(script) or
                          {"capture": capture, "cardAttempts": ["0"]})), \
             patch.object(module, "save", side_effect=lambda name, data: saved.update({name: data})):
            module.reactivation_checkpoint("repeat-lead", "repeat")
        wait = next(args[2] for args in calls if args[:2] == ("wait", "--fn"))
        check = subprocess.run(["node", "--input-type=module", "-e", "new Function('return '+process.argv[1])", wait],
                               capture_output=True, text=True)
        self.assertEqual(check.returncode, 0, check.stderr)
        snapshot_check = subprocess.run(["node", "--input-type=module", "-e", "new Function('return '+process.argv[1])", scripts[0]],
                                        capture_output=True, text=True)
        self.assertEqual(snapshot_check.returncode, 0, snapshot_check.stderr)
        self.assertEqual(saved["reactivation-checkpoint"]["event_id"], "second-step")

    def test_finished_sidebar_requires_prior_running_nested_witness(self):
        child = {"agent_id": self.a, "parent_agent_id": "agt_lead", "name": "cov-child-repeat-" + module.RUN}
        lead = {"agent_id": "agt_lead", "name": "cov-child-repeat-lead-" + module.RUN}
        capture = {"path": "/ws/LOCALMODE/chat/agt_lead", "rowId": self.a, "linkId": self.a,
                   "rowVisible": True, "running": True, "nested": True, "exactLinkCount": 1,
                   "childName": child["name"], "attemptChip": "attempt 2", "logo": True,
                   "dot": "working", "color": "3", "result": "▸ Ran code", "elapsed": "0m 02s", "capturedAt": 123}
        checkpoint = {"stage": "running", "event_id": "second-step", "turn_id": "turn-two",
                      "ui": {"capture": capture, "cardAttempts": ["0", "1"]}}
        current = {"parentGroup": False, "count": 0, "exactIdCount": 0, "id": "", "name": "",
                   "logo": False, "dot": "", "color": "", "selected": False}
        with patch.object(module, "load", side_effect=lambda label: lead if label == "repeat-lead" else
                          checkpoint if label == "reactivation-checkpoint" else child), \
             patch.object(module, "agent", return_value={**child, "state": "finished", "attempt": 1}), \
             patch.object(module, "browser_json", return_value=current), patch.object(module, "browser"), \
             patch.object(module, "save"):
            module.sidebar_child("repeat-lead", "repeat", "reactivated")
            with patch.object(module, "load", side_effect=lambda label: lead if label == "repeat-lead" else
                              {**checkpoint, "ui": {"capture": None, "cardAttempts": ["0", "1"]}}
                              if label == "reactivation-checkpoint" else child), self.assertRaises(AssertionError):
                module.sidebar_child("repeat-lead", "repeat", "reactivated")

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

    def test_started_rejects_wrong_child_name_count_or_raw_input(self):
        good = {"markerCount": 1, "ids": [self.a, self.b], "names": ["pair-a", "pair-b"],
                "toolCount": 1, "colors": ["1", "2"], "rawCode": False}
        module.assert_started_snapshot(good, [self.a, self.b], ["pair-a", "pair-b"], 1)
        module.assert_started_snapshot({**good, "toolCount": 2}, [self.a, self.b], ["pair-a", "pair-b"], 2)
        for change in ({"ids": [self.a, self.a]}, {"names": ["pair-a", "wrong"]},
                       {"ids": [self.a, self.b, "foreign"]}, {"names": ["pair-a", "pair-a"]},
                       {"names": ["pair-b", "pair-a"]}, {"colors": ["1"]},
                       {"toolCount": 3}, {"rawCode": True}):
            with self.subTest(change=change), self.assertRaises(AssertionError):
                module.assert_started_snapshot({**good, **change}, [self.a, self.b], ["pair-a", "pair-b"], 1)

    def test_started_binds_reversed_sibling_colors_by_exact_id(self):
        lead = {"agent_id": "agt_lead"}
        children = [{"agent_id": self.a, "name": "pair-a"}, {"agent_id": self.b, "name": "pair-b"}]
        shot = {"markerCount": 1, "ids": [self.b, self.a], "names": ["pair-b", "pair-a"],
                "colors": ["4", "7"], "toolCount": 1, "rawCode": False}
        history = [event("child.created", 1, "a", {"child": self.a}),
                   event("child.created", 2, "b", {"child": self.b})]
        saved = {}
        with patch.object(module, "load", side_effect=lambda label: lead if label == "pair-lead" else
                          children[0] if label == "pair-a" else children[1]), \
             patch.object(module, "events", return_value=history), \
             patch.object(module, "browser_json", return_value=shot), \
             patch.object(module, "native_create_tool_count", return_value=1), \
             patch.object(module, "browser"), \
             patch.object(module, "save", side_effect=lambda name, value: saved.update({name: value})):
            module.started_ui("pair-lead", "pair-a", "pair-b")
        self.assertEqual(saved[f"color-{self.a}"]["value"], "7")
        self.assertEqual(saved[f"color-{self.b}"]["value"], "4")

    def test_started_rejects_changed_color_for_exact_child(self):
        lead = {"agent_id": "agt_lead"}
        child = {"agent_id": self.a, "name": "pair-a"}
        shot = {"markerCount": 1, "ids": [self.a], "names": ["pair-a"],
                "colors": ["7"], "toolCount": 1, "rawCode": False}
        history = [event("child.created", 1, "a", {"child": self.a})]
        with tempfile.TemporaryDirectory(prefix="started-color-") as temp:
            (Path(temp) / f"color-{self.a}.json").write_text('{"value":"4"}')
            with patch.object(module, "OUT", Path(temp)), \
                 patch.object(module, "load", side_effect=lambda label: lead if label == "pair-lead" else
                              child if label == "pair-a" else {"value": "4"}), \
                 patch.object(module, "events", return_value=history), \
                 patch.object(module, "browser_json", return_value=shot), \
                 patch.object(module, "native_create_tool_count", return_value=1), \
                 self.assertRaises(AssertionError):
                module.started_ui("pair-lead", "pair-a")

    def test_one_child_can_have_two_saved_native_tool_entries(self):
        shot = {"markerCount": 1, "ids": [self.a], "names": ["pair-a"],
                "toolCount": 2, "colors": ["1"], "rawCode": False}
        module.assert_started_snapshot(shot, [self.a], ["pair-a"], 2)
        for ui_count, native_count in ((1, 2), (2, 1), (2, 0)):
            with self.subTest(ui_count=ui_count, native_count=native_count), self.assertRaises(AssertionError):
                module.assert_started_snapshot({**shot, "toolCount": ui_count}, [self.a], ["pair-a"], native_count)

    def test_one_native_tool_entry_can_create_two_distinct_children(self):
        both = event("item.completed", 1, "both", {"itemKind": "tool", "tool": {
            "name": "execute", "input": json.dumps({"code":
                "await tools.loom.agent_create({name:'pair-a'}); await tools.loom.agent_create({name:'pair-b'})"})}})
        self.assertEqual(module.native_create_tool_count([both]), 1)
        self.assertEqual(module.native_create_tool_count([both, both]), 2)
        searched = event("item.completed", 2, "search", {"itemKind": "tool", "tool": {
            "name": "execute", "input": json.dumps({"code":
                "const t=search({namespace:'loom',query:'agent_create'}); await t[0].call({name:'pair-a'})"})}})
        self.assertEqual(module.native_create_tool_count([searched]), 1)
        self.assertEqual(module.native_create_tool_count([searched, both]), 2)
        both["payload"]["tool"]["failed"] = True
        self.assertEqual(module.native_create_tool_count([both]), 0)

    def test_truncated_loom_execute_matches_started_fallback_only(self):
        prefix = '{"code":"const namespace=\'loom\';'
        truncated = prefix + "x" * (16 * 1024 - len(prefix))
        self.assertEqual(len(truncated), 16 * 1024)
        item = event("item.completed", 1, "truncated", {"itemKind": "tool", "tool": {
            "name": "execute", "input": truncated}})
        self.assertEqual(module.native_create_tool_count([item]), 1)
        non_loom = {**item, "payload": {"itemKind": "tool", "tool": {
            "name": "execute", "input": truncated.replace("'loom'", "'other'")}}}
        visible_other_call = {**item, "payload": {"itemKind": "tool", "tool": {
            "name": "execute", "input": prefix + "tools.loom.agent_get("}}}
        valid_json_without_create = {**item, "payload": {"itemKind": "tool", "tool": {
            "name": "execute", "input": json.dumps({"code": "const namespace='loom';"})}}}
        for bad in (non_loom, visible_other_call, valid_json_without_create):
            with self.subTest(input=bad["payload"]["tool"]["input"][:60]):
                self.assertEqual(module.native_create_tool_count([bad]), 0)

    def test_card_rejects_duplicate_wrong_color_and_raw_completion_bubble(self):
        kid = {"agent_id": self.a, "name": "pair-a"}
        good = {"cards": [{"id": self.a, "name": "pair-a", "attempt": "0", "color": "3",
                            "outcome": "completed", "delivery": "delivered"}], "rawBubble": False}
        with patch.object(module, "load", return_value={"value": "3"}):
            module.card_snapshot_ok(good, [(kid, 0)])
            for bad in ({"cards": good["cards"] * 2},
                        {"cards": [{**good["cards"][0], "color": "4"}]},
                        {"cards": [{**good["cards"][0], "id": self.b}]},
                        {"cards": [{**good["cards"][0], "name": "pair-b"}]},
                        {"rawBubble": True}):
                with self.subTest(bad=bad), self.assertRaises(AssertionError):
                    module.card_snapshot_ok({**good, **bad}, [(kid, 0)])

    def test_card_route_requires_exact_saved_child_and_hydrated_header(self):
        good = {"path": f"/ws/LOCALMODE/chat/{self.a}", "name": "pair-a", "harness": "opencode"}
        module.card_route_ok(good, self.a, "pair-a")
        for bad in ({**good, "path": "about:blank"}, {**good, "path": f"/ws/FOREIGN/chat/{self.a}"},
                    {**good, "path": f"/ws/LOCALMODE/chat/{self.b}"}, {**good, "name": ""},
                    {**good, "harness": ""}):
            with self.subTest(bad=bad), self.assertRaises(AssertionError):
                module.card_route_ok(bad, self.a, "pair-a")

    def test_new_card_and_sidebar_dom_scripts_compile(self):
        scripts = []
        card = {"count": 1, "name": "pair-a", "attempt": "0", "role": "link",
                "tabIndex": 0, "focused": True, "visible": True}
        with patch.object(module, "browser"), patch.object(module, "browser_json",
             side_effect=lambda script: (scripts.append(script) or card)):
            module.focus_exact_card("pair-a", 0)
            module.card_route()
            with patch.object(module, "load", return_value={"agent_id": self.b,
                 "name": f"cov-child-sidebar-b-{module.RUN}"}):
                module.sidebar_drag_state("sidebar-b")
        self.assertEqual(len(scripts), 3)
        self.assertIn("scrollIntoView", scripts[0])
        self.assertIn("getBoundingClientRect", scripts[0])
        for script in scripts:
            check = subprocess.run(["node", "--input-type=module", "-e", "new Function('return '+process.argv[1])", script],
                                   capture_output=True, text=True)
            self.assertEqual(check.returncode, 0, check.stderr)

    def test_child_ancestry_rejects_wrong_actual_branch_or_merge_base(self):
        prior = {"branch": "parent", "head": "a" * 40, "worktree": "/tmp/parent"}
        parent = {"branch": "parent", "worktree_path": "/tmp/parent"}
        child = {"branch": "child", "worktree_path": "/tmp/child"}
        good = {"branch": "child", "head": "b" * 40, "merge_base": prior["head"]}
        module.assert_child_ancestry(prior, parent, child, good)
        with self.assertRaisesRegex(AssertionError, "branch mismatch"):
            module.assert_child_ancestry(prior, parent, child, {**good, "branch": "wrong"})
        with self.assertRaisesRegex(AssertionError, "does not descend"):
            module.assert_child_ancestry(prior, parent, child, {**good, "merge_base": "c" * 40})
        with self.assertRaises(AssertionError):
            module.assert_child_ancestry(prior, {**parent, "worktree_path": "/tmp/other"}, child, good)

    def test_sidebar_rejects_duplicate_or_wrong_child_link(self):
        good = {"parentGroup": True, "exactIdCount": 1, "id": self.a}
        module.assert_sidebar_exact_link(good, self.a)
        for bad in ({"exactIdCount": 2}, {"id": self.b}, {"parentGroup": False}):
            with self.subTest(bad=bad), self.assertRaisesRegex(AssertionError, "missing or duplicated"):
                module.assert_sidebar_exact_link({**good, **bad}, self.a)

    def test_archive_state_requires_exact_saved_api_state(self):
        with patch.object(module, "load", return_value={"agent_id": self.a}), \
             patch.object(module, "save"), patch.object(module, "agent", return_value={"agent_id": self.a, "state": "archived"}):
            module.archive_state("side", "archived")
            with self.assertRaises(AssertionError):
                module.archive_state("side", "active")

    def test_expanded_started_requires_exact_native_rows_and_hidden_input(self):
        good = {"expanded": "true", "rows": ["Ran code", "Started cov-child-repeat"], "raw": False}
        module.expanded_bridge_ok(good, 2)
        for bad, count in (({**good, "rows": ["Started cov-child-repeat"]}, 2),
                           ({**good, "rows": ["Ran code", "Checked agent"]}, 2),
                           ({**good, "rows": ["Ran code", "Started tools.loom.agent_create"]}, 2),
                           ({**good, "raw": True}, 2), ({**good, "expanded": "false"}, 2)):
            with self.subTest(bad=bad), self.assertRaises(AssertionError):
                module.expanded_bridge_ok(bad, count)

    def test_sidebar_order_requires_two_saved_ids_and_stable_chat_route(self):
        good = {"path": f"/ws/LOCALMODE/chat/{self.b}", "nav": True, "ids": [self.a, self.b]}
        module.sidebar_order_ok(good, self.a, self.b, "before")
        module.sidebar_order_ok(good, self.a, self.b, "lifted")
        module.sidebar_order_ok({**good, "ids": [self.b, self.a]}, self.a, self.b, "moved")
        module.sidebar_order_ok({**good, "ids": [self.b, self.a]}, self.a, self.b, "after")
        module.sidebar_order_ok({**good, "ids": [self.b, self.a]}, self.a, self.b, "reload")
        for bad, stage in (({**good, "ids": [self.a, self.b]}, "after"),
                           ({**good, "ids": [self.a]}, "attempted"),
                           ({**good, "ids": [self.a, self.b, self.b]}, "attempted"),
                           ({**good, "path": "/ws/LOCALMODE/home"}, "attempted"),
                           ({**good, "path": f"/ws/FOREIGN/chat/{self.b}"}, "attempted"),
                           ({**good, "path": f"/ws/FOREIGN/chat/{self.b}"}, "lifted"),
                           ({**good, "nav": False}, "moved"),
                           ({**good, "path": f"/prefix/ws/LOCALMODE/chat/{self.b}"}, "attempted"),
                           ({**good, "nav": False}, "attempted")):
            with self.subTest(bad=bad), self.assertRaises(AssertionError):
                module.sidebar_order_ok(bad, self.a, self.b, stage)

    def test_sidebar_focus_uses_saved_identity_and_valid_browser_script(self):
        name = f"cov-child-sidebar-b-{module.RUN}"
        saved = {"agent_id": self.b, "name": name}
        route = f"/ws/LOCALMODE/chat/{self.b}"
        shot = {"path": route, "id": self.b, "name": name,
                "linkCount": 1, "handleCount": 1, "focused": True}
        scripts = []
        def inspect_script(script):
            scripts.append(script)
            return shot
        with patch.object(module, "load", return_value=saved), \
             patch.object(module, "agent", return_value=saved), \
             patch.object(module, "browser_json", side_effect=inspect_script), \
             patch.object(module, "sidebar_tab", return_value={"target_id": "owned", "url_path": route}), \
             patch.object(module, "save"):
            module.sidebar_focus("sidebar-b")
            self.assertIn(json.dumps(name), scripts[0])
            self.assertIn(json.dumps(self.b), scripts[0])
            self.assertNotIn("${RUN_ID}", scripts[0])
            check = subprocess.run(["node", "--input-type=module", "-e",
                                    'let s="";for await(const x of process.stdin)s+=x;new Function("return "+s);'],
                                   input=scripts[0], text=True, capture_output=True)
            self.assertEqual(check.returncode, 0, check.stderr)
            for bad in ({**shot, "focused": False}, {**shot, "linkCount": 0},
                        {**shot, "handleCount": 0}, {**shot, "id": self.a}):
                with self.subTest(bad=bad), patch.object(module, "browser_json", return_value=bad), \
                     self.assertRaises(AssertionError):
                    module.sidebar_focus("sidebar-b")

    def test_sidebar_tab_requires_one_active_target_and_local_route(self):
        tab = {"id": "t1", "targetId": "owned", "active": True,
               "url": f"http://127.0.0.1/ws/LOCALMODE/chat/{self.b}"}
        with patch.dict(os.environ, {"AFT_SESSION": "owned-session"}):
            good = module.sidebar_tab_result({"data": {"tabs": [tab, {"id": "t2", "targetId": "other",
                                                       "active": False, "url": "about:blank"}]}})
            self.assertEqual(good["target_id"], "owned")
            self.assertEqual(good["url_path"], f"/ws/LOCALMODE/chat/{self.b}")
            for bad in ({"data": {"tabs": [{**tab, "active": False}]}},
                        {"data": {"tabs": [tab, {**tab, "targetId": "foreign"}]}},
                        {"data": {"tabs": []}}):
                with self.subTest(bad=bad), self.assertRaises(AssertionError):
                    module.sidebar_tab_result(bad)

    def test_sidebar_key_saves_before_after_and_rejects_blank_or_foreign_tab(self):
        name = f"cov-child-sidebar-b-{module.RUN}"
        route = f"/ws/LOCALMODE/chat/{self.b}"
        saved_agent = {"agent_id": self.b, "name": name}
        tab = {"target_id": "owned", "url_path": route}
        ui = {"path": route, "nav": True, "linkCount": 1, "handleCount": 1, "focused": True}
        def run(after_tab=tab, after_ui=ui, before_ui=ui, status=0):
            saved = {}
            with patch.object(module, "load", side_effect=lambda label: tab if label == "sidebar-drag-tab" else saved_agent), \
                 patch.object(module, "sidebar_tab", side_effect=[tab, after_tab]), \
                 patch.object(module, "sidebar_drag_state", side_effect=[before_ui, after_ui]), \
                 patch.object(module.subprocess, "run", return_value=subprocess.CompletedProcess([], status)), \
                 patch.object(module, "browser"), \
                 patch.object(module, "save", side_effect=lambda name, value: saved.update({name: value})), \
                 patch.dict(os.environ, {"AFT_SESSION": "owned-session"}):
                try:
                    module.sidebar_key("sidebar-b", "Space", "lifted")
                finally:
                    self.assertIn("sidebar-key-lifted-before", saved)
                    if before_ui == ui:
                        self.assertIn("sidebar-key-lifted-after", saved)
            return saved
        self.assertEqual(run()["sidebar-key-lifted-after"]["command_status"], 0)
        for bad_tab, bad_ui in (({**tab, "target_id": "foreign"}, ui),
                                ({**tab, "url_path": "about:blank"}, {**ui, "path": "about:blank", "nav": False}),
                                (tab, {**ui, "path": "/ws/FOREIGN/chat/" + self.b})):
            with self.subTest(bad_tab=bad_tab, bad_ui=bad_ui), self.assertRaises(AssertionError):
                run(after_tab=bad_tab, after_ui=bad_ui)
        with self.assertRaisesRegex(AssertionError, "browser keyboard Space failed"):
            run(status=1)
        for bad in ({**ui, "focused": False}, {**ui, "handleCount": 0}, {**ui, "path": "about:blank"}):
            with self.subTest(before=bad), self.assertRaises(AssertionError):
                run(before_ui=bad)

    def test_parsed_sidebar_keyboard_block_runs_through_shell_with_exact_helper_args(self):
        run = next(r for r in parsed_runs() if isinstance(r, str) and "sidebar_focus sidebar-b" in r)
        self.assertNotIn("agent-browser --session \"$AFT_SESSION\" eval", run)
        with tempfile.TemporaryDirectory(prefix="child-sidebar-shell-") as temp:
            bindir = Path(temp)
            log = bindir / "calls"
            python = bindir / "python3"
            python.write_text("#!/bin/sh\nif [ \"$2\" = sidebar_focus ]; then [ \"$#\" = 3 ] && [ \"$3\" = sidebar-b ] || exit 41; printf 'focus\\n' >> \"$AFT_STUB_LOG\"; elif [ \"$2\" = sidebar_order ]; then [ \"$#\" = 5 ] && [ \"$3\" = sidebar-a ] && [ \"$4\" = sidebar-b ] && { [ \"$5\" = lifted ] || [ \"$5\" = moved ]; } || exit 43; printf 'order:%s\\n' \"$5\" >> \"$AFT_STUB_LOG\"; elif [ \"$2\" = sidebar_key ]; then [ \"$#\" = 5 ] && [ \"$3\" = sidebar-b ] && { [ \"$4:$5\" = Space:lifted ] || [ \"$4:$5\" = ArrowUp:moved ] || [ \"$4:$5\" = Space:attempted ]; } || exit 45; printf 'key:%s:%s\\n' \"$4\" \"$5\" >> \"$AFT_STUB_LOG\"; else exit 44; fi\n")
            python.chmod(0o755)
            agent_browser = bindir / "agent-browser"
            agent_browser.write_text("#!/bin/sh\n[ \"$#\" = 4 ] && [ \"$1\" = --session ] && [ \"$2\" = stub-session ] && [ \"$3\" = press ] || exit 42\nprintf 'press:%s\\n' \"$4\" >> \"$AFT_STUB_LOG\"\n")
            agent_browser.chmod(0o755)
            env = {**os.environ, "PATH": f"{temp}:{os.environ['PATH']}",
                   "AFT_TESTS_DIR": "/owned/tests", "AFT_SESSION": "stub-session", "AFT_STUB_LOG": str(log)}
            good = subprocess.run(["bash", "-e", "-c", run], env=env, capture_output=True, text=True)
            self.assertEqual(good.returncode, 0, good.stderr)
            self.assertEqual(log.read_text().splitlines(), ["focus", "key:Space:lifted", "order:lifted", "key:ArrowUp:moved", "order:moved", "key:Space:attempted"])
            bad = run.replace("sidebar_focus sidebar-b", "sidebar_focus 'cov-child-sidebar-b-${RUN_ID}'")
            failed = subprocess.run(["bash", "-e", "-c", bad], env=env, capture_output=True, text=True)
            self.assertNotEqual(failed.returncode, 0, "literal unexpanded name must not pass the real shell block")

    def test_pair_busy_command_matches_fixture_and_agent_image(self):
        root = Path(__file__).resolve().parents[3]
        fixture = json.loads((root / "tests/fixtures/slack-clone/package.json").read_text())
        dockerfile = (root / "test/local-mode/Dockerfile").read_text()
        prompt = parsed_pair_busy_prompt()
        self.assertEqual(fixture["scripts"]["test"], "node --test")
        self.assertIn("FROM local-mode AS agents\nCOPY --from=docker.io/library/node:24-bookworm-slim /usr/local/bin/node /usr/local/bin/node", dockerfile)
        self.assertIn("Run node --test once", prompt)
        self.assertNotIn("npm test", prompt)


if __name__ == "__main__":
    unittest.main()
