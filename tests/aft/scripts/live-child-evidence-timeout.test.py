#!/usr/bin/env python3
"""Offline checks for the default-nine child completion failure snapshot."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name("live-child-evidence.py")
with tempfile.TemporaryDirectory(prefix="aft-child-timeout-offline-") as module_work:
    with patch.dict(os.environ, {"AFT_WORK_DIR": module_work, "AFT_API_URL": "http://127.0.0.1:1", "AFT_WS": "LOCALMODE"}):
        spec = importlib.util.spec_from_file_location("live_child_evidence", SCRIPT)
        child = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(child)


def parsed_wait_command():
    suite = SCRIPT.parents[1] / "live-agent-flow-suites/children.test.yaml"
    loader = Path(os.environ.get("AFT_DIR", "/Users/tyson/codebase/code-agents/testing-app")) / "dist/runner.js"
    code = """import {pathToFileURL} from 'node:url';
const [loader,file]=process.argv.slice(2);
const suite=(await import(pathToFileURL(loader).href)).loadSuite(file);
console.log(JSON.stringify(suite.tests.map(t=>t.steps.filter(s=>s.run?.includes('pair_timeout')).map(s=>s.run))));"""
    env = {**os.environ, "RUN_ID": "offline", "AFT_WS": "LOCALMODE", "AFT_BASE_URL": "http://127.0.0.1:1",
           "AFT_API_URL": "http://127.0.0.1:1", "AFT_AGENT_FLOW_REPO": "/owned/repo",
           "AFT_REAL_BACKEND": "opencode", "AFT_REAL_MODEL": "offline",
           "AFT_SELECT_AGENT_MODEL": "/bin/true", "AFT_NATIVE_MODEL_PROBE": "/bin/true"}
    raw = subprocess.check_output(["node", "--input-type=module", "-", str(loader), str(suite)],
                                  input=code, env=env, text=True)
    found = json.loads(raw)
    assert len(found) == 3 and len(found[0]) == 1 and not found[1] and not found[2]
    return found[0][0]


class PairTimeoutTests(unittest.TestCase):
    def test_error_categories_are_fixed_and_never_export_raw_error(self):
        for raw, category in (("opencode: auth_failed (401 Unauthorized): private details", "opencode_auth_failed"),
                              ("opencode: harness_down (503 Unavailable): private details", "opencode_harness_down"),
                              ("model not found\nprivate details", "model_not_found"),
                              ("private unknown failure", "unknown")):
            value = child.safe_turn_error(raw)
            self.assertEqual(value["category"], category)
            self.assertEqual(value["byte_length"], len(raw.encode()))
            self.assertNotIn("private", json.dumps(value))
        self.assertEqual(child.safe_turn_error("")["category"], "unknown")

    def test_exact_pair_failure_snapshot_excludes_summary_text_and_foreign_rows(self):
        run = "offline"
        lead_id, a_id, b_id = "agt_lead", "agt_a", "agt_b"
        names = [f"aft-child-{letter}-{run}" for letter in "ab"]
        agents = {
            lead_id: {"agent_id": lead_id, "name": f"aft-child-lead-{run}", "state": "idle", "attempt": 0},
            a_id: {"agent_id": a_id, "name": names[0], "parent_agent_id": lead_id, "root_agent_id": lead_id,
                   "state": "finished", "attempt": 0},
            b_id: {"agent_id": b_id, "name": names[1], "parent_agent_id": lead_id, "root_agent_id": lead_id,
                   "state": "active", "attempt": 0},
        }
        event_rows = {
            lead_id: [{"agent_id": lead_id, "event_id": "completed-a", "seq": 10, "kind": "task_completed",
                       "turn_id": "", "payload": {"child": a_id, "attempt": 0, "outcome": "failed", "head": "abc",
                                                       "summary": "private summary"}},
                      {"agent_id": lead_id, "event_id": "delivery-a", "seq": 11, "kind": "message.delivered",
                       "turn_id": "turn", "payload": {"text": "private text", "completions": [{"child": a_id, "attempt": 0}]}}],
            a_id: [{"agent_id": a_id, "event_id": "end-a", "seq": 6, "kind": "agent.turn_completed",
                    "turn_id": "turn-a", "payload": {"stopReason": "failed", "error": "private child error"}}],
            b_id: [],
        }
        window = {key: "2026-10-07T16:17:00Z" for key in (
            "AFT_PAIR_WAIT_FIRST_STARTED_AT", "AFT_PAIR_WAIT_FIRST_ENDED_AT",
            "AFT_PAIR_WAIT_SECOND_STARTED_AT", "AFT_PAIR_WAIT_SECOND_ENDED_AT")}
        with tempfile.TemporaryDirectory() as tmp, patch.object(child, "WORK", Path(tmp)), \
             patch.dict(os.environ, {"RUN_ID": run, **window}), patch.object(child, "agent", side_effect=agents.__getitem__), \
             patch.object(child, "events", side_effect=event_rows.__getitem__), \
             patch.object(child, "pair_timeout_dom", return_value={"available": True, "card_count": 0, "lead_route_matches": True}), \
             patch.object(child, "native_failure"):
            (child.WORK / "pair-lead").write_text(lead_id)
            for name, agent_id in zip(names, (a_id, b_id)):
                (child.WORK / f"pair-{name}.id").write_text(agent_id)
            child.pair_timeout("pair-lead", *names)
            self.assertEqual(child.load("pair-timeout-wait"), window)
            api = child.load("pair-timeout-api")
            self.assertEqual(api["events"][a_id][0]["payload"]["error"]["category"], "unknown")
            self.assertEqual(api["events"][lead_id][0]["payload"]["summary_present"], True)
            self.assertEqual(child.load("pair-timeout-dom")["card_count"], 0)
            capture = child.load("pair-timeout-capture")
            self.assertEqual((capture["api_status"], capture["dom_status"]), ("complete", "available"))
            self.assertRegex(capture["capture_started_at"], r"^\d{4}-\d\d-\d\dT.*Z$")
            self.assertRegex(capture["capture_ended_at"], r"^\d{4}-\d\d-\d\dT.*Z$")
            self.assertLessEqual(capture["capture_started_at"], capture["capture_ended_at"])
            self.assertNotIn("private", json.dumps(api))
            event_rows[a_id][0]["agent_id"] = "agt_foreign"
            with self.assertRaisesRegex(AssertionError, "foreign failure event"):
                child.pair_timeout("pair-lead", *names)

    def test_dom_projection_keeps_only_card_metadata_and_exact_route(self):
        real_check_output = subprocess.check_output
        js = """const code=Buffer.from(process.argv[1],'base64').toString();
globalThis.location={pathname:process.argv[2]};
const card={dataset:{outcome:'failed',delivery:'delivered'},textContent:'aft-child-a-offline private body'};
const tray={querySelectorAll:(selector)=>selector.includes('running')?[{},{}]:[{},{}]};
globalThis.document={querySelectorAll:()=>[card],querySelector:(selector)=>selector.includes('agent-tray')?tray:null};
console.log(eval(code));"""
        def evaluate(args, text, timeout):
            self.assertEqual(args[:4], ["agent-browser", "--session", "owned", "eval"])
            self.assertEqual(timeout, 10)
            return real_check_output(["node", "--input-type=module", "-e", js, args[-1],
                                      "/ws/LOCALMODE/chat/agt_lead"], text=True)
        with patch.dict(os.environ, {"AFT_SESSION": "owned"}), patch.object(child.subprocess, "check_output", side_effect=evaluate):
            row = child.pair_timeout_dom("agt_lead", ["aft-child-a-offline", "aft-child-b-offline"])
        self.assertTrue(row["lead_route_matches"])
        self.assertTrue(row["available"])
        self.assertEqual(row["card_count"], 1)
        self.assertEqual(row["cards"], [{"outcome": "failed", "delivery": "delivered",
                                         "child_name_matches": [True, False]}])
        self.assertEqual(row["tray_running_count"], 2)
        self.assertNotIn("private body", json.dumps(row))

    def test_dom_timeout_keeps_api_and_only_safe_failure_receipts(self):
        run = "offline"
        lead_id, a_id, b_id = "agt_lead", "agt_a", "agt_b"
        names = [f"aft-child-{letter}-{run}" for letter in "ab"]
        agents = {
            lead_id: {"agent_id": lead_id, "name": f"aft-child-lead-{run}", "state": "active"},
            a_id: {"agent_id": a_id, "name": names[0], "parent_agent_id": lead_id, "root_agent_id": lead_id},
            b_id: {"agent_id": b_id, "name": names[1], "parent_agent_id": lead_id, "root_agent_id": lead_id},
        }
        wait_window = {key: "2026-10-07T16:17:00Z" for key in (
            "AFT_PAIR_WAIT_FIRST_STARTED_AT", "AFT_PAIR_WAIT_FIRST_ENDED_AT",
            "AFT_PAIR_WAIT_SECOND_STARTED_AT", "AFT_PAIR_WAIT_SECOND_ENDED_AT")}
        timeout = subprocess.TimeoutExpired(cmd="agent-browser", timeout=10, stderr=b"private browser error")
        with tempfile.TemporaryDirectory() as tmp, patch.object(child, "WORK", Path(tmp)), \
             patch.dict(os.environ, {"RUN_ID": run, "AFT_SESSION": "owned", **wait_window}), \
             patch.object(child, "agent", side_effect=agents.__getitem__), \
             patch.object(child, "events", return_value=[]), \
             patch.object(child.subprocess, "check_output", side_effect=timeout) as browser, \
             patch.object(child, "native_failure"):
            (child.WORK / "pair-lead").write_text(lead_id)
            for name, agent_id in zip(names, (a_id, b_id)):
                (child.WORK / f"pair-{name}.id").write_text(agent_id)
            child.pair_timeout("pair-lead", *names)
            self.assertEqual(browser.call_args.kwargs["timeout"], 10)
            self.assertEqual(child.load("pair-timeout-api")["events"], {lead_id: [], a_id: [], b_id: []})
            self.assertEqual(child.load("pair-timeout-dom"), {"available": False, "failure_category": "timeout"})
            capture = child.load("pair-timeout-capture")
            self.assertEqual((capture["api_status"], capture["dom_status"], capture["dom_failure_category"]),
                             ("complete", "unavailable", "timeout"))
            for key in ("capture_started_at", "api_started_at", "api_ended_at", "dom_started_at",
                        "dom_ended_at", "capture_ended_at"):
                self.assertRegex(capture[key], r"^\d{4}-\d\d-\d\dT.*Z$")
            self.assertNotIn("private", "".join(p.read_text() for p in child.WORK.glob("pair-timeout-*.json")))

    def test_total_capture_budget_preserves_partial_api_and_original_wait_window(self):
        window = {key: "2026-10-07T16:17:00Z" for key in (
            "AFT_PAIR_WAIT_FIRST_STARTED_AT", "AFT_PAIR_WAIT_FIRST_ENDED_AT",
            "AFT_PAIR_WAIT_SECOND_STARTED_AT", "AFT_PAIR_WAIT_SECOND_ENDED_AT")}
        def partial_api(_label, _names):
            child.save("pair-timeout-api", {"lead": {"agent_id": "agt_lead"}, "events": {}})
            raise child.CaptureBudgetExpired()
        with tempfile.TemporaryDirectory() as tmp, patch.object(child, "WORK", Path(tmp)), \
             patch.dict(os.environ, window), patch.object(child, "pair_timeout_api", side_effect=partial_api), \
             patch.object(child, "pair_timeout_dom") as browser:
            child.pair_timeout("pair-lead", "a", "b")
            browser.assert_not_called()
            self.assertEqual(child.load("pair-timeout-wait"), window)
            self.assertEqual(child.load("pair-timeout-api")["lead"]["agent_id"], "agt_lead")
            receipt = child.load("pair-timeout-capture")
            self.assertEqual((receipt["api_status"], receipt["api_failure_category"], receipt["dom_status"]),
                             ("unavailable", "capture_budget_exhausted", "not_attempted"))
            self.assertLessEqual(receipt["capture_started_at"], receipt["capture_ended_at"])
            self.assertNotIn("private", json.dumps(receipt))

    def test_cleanup_evidence_projects_safe_late_native_error(self):
        agent_id = "agt_child"
        errors = ["opencode: auth_failed (401): private credential", "private unknown failure"]
        with tempfile.TemporaryDirectory() as tmp, patch.object(child, "WORK", Path(tmp)), \
             patch.object(child, "agent", return_value={"agent_id": agent_id, "finished_at": "2026-10-07T16:17:57Z"}):
            for index, error in enumerate(errors):
                event = {"event_id": f"end-{index}", "seq": 6, "kind": "agent.turn_completed",
                         "turn_id": "turn", "payload": {"stopReason": "failed", "error": error}}
                with patch.object(child, "events", return_value=[event]):
                    child.evidence(f"final-{index}", agent_id)
                saved = child.load(f"final-{index}-events")[0]["payload"]
                self.assertEqual(saved["error_category"], ("opencode_auth_failed", "unknown")[index])
                self.assertEqual(saved["error_byte_length"], len(error.encode()))
                self.assertEqual(saved["error_sha256"], child.safe_turn_error(error)["sha256"])
                self.assertTrue(saved["error_present"])
                self.assertNotIn("private", json.dumps(saved))

    def test_native_probe_uses_exact_saved_failed_turn_and_rejects_raw_or_timeout(self):
        child_id, lead_id = "agt_child", "agt_lead"
        digest = "a" * 64
        event = {"kind": "agent.turn_completed",
                 "event_id": "agent.turn_completed::ses_child:turn_child", "seq": 6,
                 "turn_id": "turn_child", "payload": {"stopReason": "failed", "error_sha256": digest}}
        safe = {"agent_id": child_id, "native_id": "ses_child", "native_root": "",
                "registry_requested_model": None, "native_session_selected_model": None,
                "native_service_default_model": "opencode/exo-free", "model_evidence": "selected-and-default-only",
                "status": "linked", "native_failure": {"event_id": "evt_native", "seq": 9,
                  "session_id": "ses_child", "type": "provider.auth", "status": 401,
                  "message_byte_length": 80, "message_sha256": digest, "watermark": 9},
                "loom_turn": {"event_id": "agent.turn_completed::ses_child:turn_child",
                              "seq": 6, "turn_id": "turn_child",
                              "error_sha256": digest}}
        with tempfile.TemporaryDirectory() as tmp, patch.object(child, "WORK", Path(tmp)), \
             patch.dict(os.environ, {"AFT_TESTS_DIR": tmp}):
            response = subprocess.CompletedProcess([], 0, json.dumps(safe), "private stderr")
            with patch.object(child.subprocess, "run", return_value=response) as command:
                child.native_failure("final", child_id, lead_id, [event])
            self.assertEqual(command.call_args.args[0][-6:],
                             [child_id, lead_id, "agent.turn_completed::ses_child:turn_child",
                              "6", "turn_child", digest])
            self.assertEqual(command.call_args.kwargs["timeout"], 8)
            self.assertEqual(child.load(f"pair-native-failure-{child_id}-final"), safe)
            self.assertNotIn("private", (child.WORK / f"pair-native-failure-{child_id}-final.json").read_text())
            with patch.object(child.subprocess, "run", return_value=subprocess.CompletedProcess(
                    [], 0, json.dumps({**safe, "raw_error": "private"}), "")):
                child.native_failure("final", child_id, lead_id, [event])
            self.assertEqual(child.load(f"pair-native-failure-{child_id}-final")["status"], "unavailable")
            with patch.object(child.subprocess, "run", side_effect=subprocess.TimeoutExpired("probe", 8,
                                                                                        stderr=b"private")):
                child.native_failure("wait", child_id, lead_id, [event])
            self.assertEqual(child.load(f"pair-native-failure-{child_id}-wait")["status"], "unavailable")
            with patch.object(child.subprocess, "run") as command:
                child.native_failure("wait", child_id, lead_id, [event], deadline=child.time.monotonic())
            command.assert_not_called()
            self.assertEqual(child.load(f"pair-native-failure-{child_id}-wait")["reason"],
                             "capture_budget_exhausted")

    def test_cleanup_invokes_pair_native_probe_before_archive(self):
        lead_id, child_id = "agt_lead", "agt_child"
        with tempfile.TemporaryDirectory() as tmp, patch.object(child, "WORK", Path(tmp)), \
             patch.dict(os.environ, {"RUN_ID": "af12345678"}):
            (child.WORK / "pair-lead").write_text(lead_id)
            (child.WORK / "pair-aft-child-a-af12345678.id").write_text(child_id)
            agents = {lead_id: {"name": "aft-child-lead-af12345678", "state": "finished"},
                      child_id: {"name": "aft-child-a-af12345678", "state": "finished"}}
            def evidence(label, agent_id):
                child.save(f"{label}-events", [{"kind": "agent.turn_completed",
                                                  "event_id": "agent.turn_completed::ses_child:turn_child",
                                                  "seq": 6, "turn_id": "turn_child",
                                                  "payload": {"stopReason": "failed", "error_sha256": "a" * 64}}])
                return {}, [{"agent_id": agent_id}]
            with patch.object(child, "get", return_value={"agents": [], "next": None}), \
                 patch.object(child, "agent", side_effect=agents.__getitem__), \
                 patch.object(child, "evidence", side_effect=evidence), \
                 patch.object(child, "native_failure") as probe, \
                 patch.object(child.urllib.request, "urlopen") as archive:
                child.cleanup()
            probe.assert_called_once()
            self.assertEqual(probe.call_args.args[:3], ("final", child_id, lead_id))
            self.assertEqual(probe.call_args.args[3][0]["event_id"],
                             "agent.turn_completed::ses_child:turn_child")
            self.assertEqual(archive.call_count, 2)

    def test_actual_parsed_wait_wrapper_preserves_failure_and_only_snapshots_on_failure(self):
        command = parsed_wait_command()
        self.assertIn("r.length === 2", command)
        self.assertIn("e.dataset.outcome === 'completed'", command)
        self.assertIn("e.dataset.delivery === 'delivered'", command)
        self.assertNotIn("--timeout", command)
        self.assertNotIn("sleep", command)
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)
            browser = path / "agent-browser"
            python = path / "python3"
            browser.write_text("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$TEST_BROWSER_ARGS\"\nprintf 'timeout=%s\\n' \"$AGENT_BROWSER_DEFAULT_TIMEOUT\" >> \"$TEST_BROWSER_ARGS\"\nexit \"$TEST_BROWSER_EXIT\"\n")
            python.write_text("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TEST_PYTHON_ARGS\"\nprintf '%s\\n' \"$AFT_PAIR_WAIT_FIRST_STARTED_AT\" \"$AFT_PAIR_WAIT_FIRST_ENDED_AT\" \"$AFT_PAIR_WAIT_SECOND_STARTED_AT\" \"$AFT_PAIR_WAIT_SECOND_ENDED_AT\" >> \"$TEST_PYTHON_ARGS\"\n")
            browser.chmod(0o755)
            python.chmod(0o755)
            env = {**os.environ, "PATH": f"{tmp}:{os.environ['PATH']}", "AFT_SESSION": "owned-session",
                   "AFT_TESTS_DIR": tmp, "RUN_ID": "offline", "TEST_BROWSER_ARGS": str(path / "browser.args"),
                   "TEST_PYTHON_ARGS": str(path / "python.args"), "TEST_BROWSER_EXIT": "17",
                   "AGENT_BROWSER_DEFAULT_TIMEOUT": "30000"}
            result = subprocess.run(["bash", "-c", command], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 17, result.stderr)
            self.assertEqual((path / "browser.args").read_text().count("wait\n--fn\n"), 2)
            self.assertEqual((path / "browser.args").read_text().count("timeout=30000"), 2)
            self.assertIn("pair_timeout\npair-lead\naft-child-a-offline\naft-child-b-offline", (path / "python.args").read_text())
            self.assertEqual((path / "python.args").read_text().count("2026-"), 4)
            (path / "python.args").unlink()
            (path / "browser.args").unlink()
            env["TEST_BROWSER_EXIT"] = "0"
            result = subprocess.run(["bash", "-c", command], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((path / "browser.args").read_text().count("wait\n--fn\n"), 1)
            self.assertFalse((path / "python.args").exists())


if __name__ == "__main__":
    unittest.main()
