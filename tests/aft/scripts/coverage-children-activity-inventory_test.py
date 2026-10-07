#!/usr/bin/env python3
"""Fail-closed offline checks for the saved native Loom tool inventory."""

import copy
import atexit
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


os.environ.setdefault("RUN_ID", "af12345678")
os.environ.setdefault("AFT_WS", "LOCALMODE")
os.environ.setdefault("AFT_API_URL", "http://127.0.0.1:1")
_work = tempfile.TemporaryDirectory(prefix="child-inventory-")
atexit.register(_work.cleanup)
os.environ.setdefault("AFT_WORK_DIR", _work.name)
spec = importlib.util.spec_from_file_location("child_activity", Path(__file__).with_name("coverage-children-activity.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

NAMES = ["agent_archive", "agent_create", "agent_get", "agent_list", "agent_send", "github_read"]


def row(kind, seq, event_id, payload, turn_id=""):
    return {"agent_id": "agt_lead", "kind": kind, "seq": seq, "event_id": event_id,
            "payload": payload, "turn_id": turn_id}


def fixture():
    native, turn = "ses_owned", "turn_first"
    child_name = f"cov-child-repeat-{module.RUN}"
    lead = {"agent_id": "agt_lead", "harness_session_id": native, "harness_session_root": ""}
    child = {"agent_id": "agt_child", "name": child_name,
             "parent_agent_id": "agt_lead", "created_by_id": "agt_lead"}
    history = [
        row("turn.started", 1, f"turn.started::{native}:{turn}", {"session": native}, turn),
        row("item.completed", 3, f"item.completed::{native}:msg_inv/tool/call_1",
            {"session": native, "itemKind": "tool", "itemId": "msg_inv/tool/call_1",
             "tool": {"name": "execute", "input": json.dumps({"code": "return Object.keys(tools.loom)"}),
                      "output": json.dumps(NAMES)}}, turn),
        row("child.created", 4, "child.created:agt_child", {"child": "agt_child"}),
        row("item.completed", 5, f"item.completed::{native}:msg_create/tool/call_2",
            {"session": native, "itemKind": "tool", "itemId": "msg_create/tool/call_2",
             "tool": {"name": "execute", "input": json.dumps({"code":
                      f"return await tools.loom.agent_create({{name:'{child_name}',brief:'work'}})"}),
                      "output": "created"}}, turn),
    ]
    return lead, child, history


class NativeInventory(unittest.TestCase):
    def test_exact_owned_native_result_and_saved_wrapper(self):
        lead, child, history = fixture()
        proof = module.inventory_result(lead, child, history)
        self.assertEqual(proof["installed_names"], NAMES)
        self.assertEqual(proof["event_id"], history[1]["event_id"])
        self.assertEqual(proof["native_session_id"], "ses_owned")
        self.assertEqual(proof["input"], "return Object.keys(tools.loom)")
        self.assertEqual(proof["create_event_id"], history[3]["event_id"])
        with patch.object(module, "load", side_effect=lambda label: {"agent_id": lead["agent_id"]}
                          if label == "repeat-lead" else {"agent_id": child["agent_id"]}), \
             patch.object(module, "agent", side_effect=lambda aid: lead if aid == lead["agent_id"] else child), \
             patch.object(module, "events", return_value=history), patch.object(module, "save") as saved:
            module.inventory("repeat-lead", "repeat")
            saved.assert_called_once_with("installed-loom-tools", proof)

    def test_rejects_missing_foreign_failed_echo_and_wrong_output(self):
        lead, child, original = fixture()

        def reject(edit):
            history = copy.deepcopy(original)
            edit(history)
            with self.assertRaises(AssertionError):
                module.inventory_result(lead, child, history)

        reject(lambda h: h.pop(1))
        reject(lambda h: h.__setitem__(1, row("item.completed", 2, "echo", {"itemKind": "message",
               "text": json.dumps(NAMES)}, "turn_first")))
        reject(lambda h: h[1]["payload"]["tool"].update(failed=True))
        reject(lambda h: h[1]["payload"]["tool"].update(output=json.dumps(NAMES[:-1])))
        reject(lambda h: h[1]["payload"]["tool"].update(output=json.dumps(NAMES + ["extra"])))
        reject(lambda h: h[1]["payload"]["tool"].update(output="Listed: " + json.dumps(NAMES)))
        reject(lambda h: h[1]["payload"]["tool"].update(input=json.dumps({"code":
               "return Object.keys(tools.loom); await fetch('http://example.test')"})))
        reject(lambda h: h[1].update(agent_id="agt_foreign"))
        reject(lambda h: h[1]["payload"].update(session="ses_foreign"))
        reject(lambda h: h[1].update(event_id="item.completed:/foreign:ses_owned:msg_inv/tool/call_1"))
        reject(lambda h: h[1].update(turn_id="turn_foreign"))
        reject(lambda h: h[1].update(seq=6))
        reject(lambda h: h[0].update(event_id="turn.started:foreign:ses_owned:turn_first"))
        reject(lambda h: h[2]["payload"].update(child="agt_foreign"))
        reject(lambda h: h[2].update(event_id="child.created:agt_foreign"))
        reject(lambda h: h.append(copy.deepcopy(h[1])))

    def test_parsed_suite_keeps_three_cases_and_no_prompt_answer(self):
        tests = Path(__file__).resolve().parents[1]
        loader = Path(os.environ.get("AFT_DIR", "/Users/tyson/codebase/code-agents/testing-app")) / "dist/runner.js"
        code = """import {pathToFileURL} from 'node:url';
const [loader,file]=process.argv.slice(2);
const {loadSuite}=await import(pathToFileURL(loader).href);
const suite=loadSuite(file);
console.log(JSON.stringify(suite.tests.map(t=>({name:t.name,steps:t.steps}))));"""
        env = {**os.environ, "AFT_BASE_URL": "http://127.0.0.1:1", "AFT_REAL_MODEL": "offline",
               "AFT_NATIVE_MODEL_PROBE": "/bin/true", "AFT_SELECT_AGENT_MODEL": "/bin/true"}
        suite = tests / "live-agent-coverage-suites/children-activity.test.yaml"
        rows = json.loads(subprocess.check_output(["node", "--input-type=module", "-", str(loader), str(suite)],
                                               input=code, env=env, text=True))
        self.assertEqual(len(rows), 3)
        first = rows[0]["steps"]
        prompts = [step["fill"]["value"] for step in first if isinstance(step, dict) and
                   isinstance(step.get("fill"), dict) and step["fill"].get("label") == "Message"]
        self.assertIn("return Object.keys(tools.loom)", prompts[0])
        for name in ("agent_archive", "agent_list", "agent_send", "github_read"):
            self.assertNotIn(name, prompts[0])
        self.assertEqual(sum(" inventory repeat-lead repeat" in step.get("run", "")
                             for row in rows for step in row["steps"] if isinstance(step, dict)), 1)


if __name__ == "__main__":
    unittest.main()
