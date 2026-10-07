#!/usr/bin/env python3
"""Offline negative fixtures for the live child-queue evidence predicates."""

import importlib.util
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("queue", Path(__file__).with_name("coverage-children-queue.py"))
queue = importlib.util.module_from_spec(spec)
spec.loader.exec_module(queue)


def delivery(seq, text, sender, key, child="agt_child"):
    return {"agent_id": child, "kind": "message.delivered", "seq": seq,
            "event_id": f"delivered:{key}", "payload": {"text": text, "sender": sender, "inputKey": key}}


def parsed_queue_prompts():
    tests = Path(__file__).resolve().parents[1]
    loader = Path(os.environ.get("AFT_DIR", "/Users/tyson/codebase/code-agents/testing-app")) / "dist/runner.js"
    code = """import {pathToFileURL} from 'node:url';
const [loader,file]=process.argv.slice(2);
const {loadSuite}=await import(pathToFileURL(loader).href);
const suite=loadSuite(file);
console.log(JSON.stringify(suite.tests.flatMap(t=>t.steps).filter(s=>s.fill?.label==='Message'||s.run).map(s=>s.fill?.value||s.run)));"""
    env = {**os.environ, "RUN_ID": queue.RUN, "AFT_WS": "LOCALMODE", "AFT_BASE_URL": "http://127.0.0.1:1",
           "AFT_REAL_MODEL": "offline", "AFT_NATIVE_MODEL_PROBE": "/bin/true",
           "AFT_NATIVE_SESSION_PROBE": "/bin/true", "AFT_SELECT_AGENT_MODEL": "/bin/true"}
    output = subprocess.check_output(["node", "--input-type=module", "-", str(loader),
                                      str(tests / "live-agent-coverage-suites/children-queue.test.yaml")],
                                     input=code, env=env, text=True)
    return json.loads(output)


def parsed_queue_waits():
    tests = Path(__file__).resolve().parents[1]
    loader = Path(os.environ.get("AFT_DIR", "/Users/tyson/codebase/code-agents/testing-app")) / "dist/runner.js"
    code = """import {pathToFileURL} from 'node:url';
const [loader,file]=process.argv.slice(2);
const {loadSuite}=await import(pathToFileURL(loader).href);
const suite=loadSuite(file);
console.log(JSON.stringify(suite.tests.flatMap(t=>t.steps).filter(s=>s.wait?.fn).map(s=>s.wait.fn)));"""
    env = {**os.environ, "RUN_ID": queue.RUN, "AFT_WS": "LOCALMODE", "AFT_BASE_URL": "http://127.0.0.1:1",
           "AFT_REAL_MODEL": "offline", "AFT_NATIVE_MODEL_PROBE": "/bin/true",
           "AFT_NATIVE_SESSION_PROBE": "/bin/true", "AFT_SELECT_AGENT_MODEL": "/bin/true"}
    output = subprocess.check_output(["node", "--input-type=module", "-", str(loader),
                                      str(tests / "live-agent-coverage-suites/children-queue.test.yaml")],
                                     input=code, env=env, text=True)
    return json.loads(output)


class QueueOracleTests(unittest.TestCase):
    def test_native_send_settles_with_reply_without_queue_marker(self):
        lead, child = "agt_lead", "agt_child"
        marker = "QUEUE-P1-"
        call = {"agent_id": lead, "kind": "item.completed", "event_id": "send", "seq": 10,
                "turn_id": "turn-two", "payload": {"itemKind": "tool", "tool": {"name": "agent_send",
                    "input": json.dumps({"agent": child, "text": marker + "sample"}), "output": "Message sent"}}}
        reply = {"agent_id": lead, "kind": "item.completed", "event_id": "reply", "seq": 11,
                 "turn_id": "turn-two", "payload": {"itemKind": "message", "text": "Message sent once; receipt recorded."}}
        end = {"agent_id": lead, "kind": "agent.turn_completed", "event_id": "end", "seq": 12,
               "turn_id": "turn-two", "payload": {"stopReason": "end_turn"}}
        self.assertEqual(queue.settled_send_turn([call, reply, end], lead, child, marker), (call, end, [reply]))
        cases = (([{**call, "agent_id": "agt_foreign"}, reply, end], "missing or duplicate"),
                 ([{**call, "payload": {**call["payload"], "tool": {**call["payload"]["tool"],
                     "input": json.dumps({"agent": "agt_foreign", "text": marker + "sample"})}}}, reply, end], "missing or duplicate"),
                 ([{**call, "payload": {**call["payload"], "tool": {**call["payload"]["tool"],
                     "input": json.dumps({"agent": child, "text": "foreign"})}}}, reply, end], "missing or duplicate"),
                 ([{**call, "payload": {**call["payload"], "tool": {**call["payload"]["tool"], "failed": True}}}, reply, end], "failed or lacks"),
                 ([call, end], "lacks a completed reply"),
                 ([call, {**reply, "agent_id": "agt_foreign"}, end], "lacks a completed reply"),
                 ([call, reply, {**end, "turn_id": "wrong"}], "did not complete"),
                 ([call, reply, {**end, "payload": {"stopReason": "failed"}}], "did not complete"))
        for rows, message in cases:
            with self.subTest(rows=rows), self.assertRaisesRegex(AssertionError, message):
                queue.settled_send_turn(rows, lead, child, marker)

    def test_parsed_parent_send_waits_accept_reply_without_marker(self):
        waits = parsed_queue_waits()
        for marker in ("QUEUE-P1-", "QUEUE-P2-", "QUEUE-P3-", "QUEUE-P3B-"):
            matching = [w for w in waits if marker + queue.RUN in w and "[data-state=idle]" in w]
            self.assertEqual(len(matching), 1, marker)
            expression = matching[0]
            code = """const expr=process.argv[1], marker=process.argv[2], haveReply=process.argv[3]==='true';
const rows=[{dataset:{kind:'user'},textContent:'Use agent_send with '+marker},
  ...(haveReply?[{dataset:{kind:'agent'},textContent:'Message sent once; receipt msg_123, state waiting.'}]:[])];
const document={querySelector:()=>({}),querySelectorAll:()=>rows};
console.log(JSON.stringify(new Function('document','return '+expr)(document)));"""
            def shown(have_reply):
                result = subprocess.run(["node", "--input-type=module", "-e", code, expression,
                                         marker + queue.RUN, str(have_reply).lower()], capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                return json.loads(result.stdout)
            self.assertTrue(shown(True), marker)
            self.assertFalse(shown(False), marker)

    def test_finished_child_after_real_parent_receipt_is_inconclusive_for_fifo(self):
        call = {"event_id": "send-p1"}
        wait = {"kind": "message.waiting", "event_id": "agt_child:send:agent_tool-ABCDEFGHIJKLMNOPQRSTUVWXY2:message.waiting",
                "payload": {"reason": "agent:agt_lead"}}
        child = {"agent_id": "agt_child", "state": "finished", "running_turn_id": None,
                 "attempt": 0, "waiting_messages": []}
        saved = {}
        with patch.object(queue, "settled_lead_send", return_value=call), \
             patch.object(queue, "native_send_result", return_value={"message_id": "msg_exact"}), \
             patch.object(queue, "events", return_value=[wait]), \
             patch.object(queue, "agent", return_value=child), \
             patch.object(queue, "identity", side_effect=lambda label: "agt_lead" if label == "lead" else "agt_child"), \
             patch.object(queue, "load", return_value={"turn": "turn-busy"}), \
             patch.object(queue, "native_wait_request", return_value="agent_tool-ABCDEFGHIJKLMNOPQRSTUVWXY2"), \
             patch.object(queue, "save", side_effect=lambda name, value: saved.update({name: value})), \
             self.assertRaisesRegex(AssertionError, "inconclusive: child finished"):
            queue.fifo_parent()
        self.assertEqual(saved["fifo-parent-precondition"]["state"], "finished")
        self.assertEqual(saved["fifo-parent-precondition"]["saved_waiting_event_ids"], [wait["event_id"]])
        self.assertEqual(saved["fifo-parent-receipt"]["tool_event_id"], "send-p1")

    def test_started_group_binds_one_child_to_two_adjacent_saved_tool_entries(self):
        search = {"kind": "item.completed", "event_id": "search", "seq": 10,
                  "payload": {"itemKind": "tool", "tool": {"name": "execute", "input": json.dumps({
                      "code": "return await tools.loom.search({namespace:'loom',query:'agent_create'})"})}}}
        create = {"kind": "item.completed", "event_id": "create", "seq": 11,
                  "payload": {"itemKind": "tool", "tool": {"name": "execute", "input": json.dumps({
                      "code": f"return await tools.loom.agent_create({{name:'{queue.CHILD_NAME}'}})"})}}}
        marker = {"kind": "child.created", "event_id": "created", "seq": 12,
                  "payload": {"child": "agt_child", "name": queue.CHILD_NAME}}
        self.assertEqual(queue.started_create_role(search), "code")
        self.assertEqual(queue.started_create_role(create), "create")
        group = queue.started_group([search, create, marker], "agt_child", "create")
        self.assertEqual(group, {"marker_event_id": "created", "entries": [
            {"event_id": "search", "role": "code"}, {"event_id": "create", "role": "create"}]})
        queue.started_group_ui_ok({"toolCount": 2, "expanded": "false", "expandedRows": []}, group, "collapsed")
        expanded = {"toolCount": 2, "expanded": "true", "expandedRows": [
            {"label": "Ran code", "status": "completed", "inGroup": "true"},
            {"label": "Started " + queue.CHILD_NAME, "status": "completed", "inGroup": "true"}]}
        queue.started_group_ui_ok(expanded, group, "expanded")
        for bad in ({**expanded, "toolCount": 1},
                    {**expanded, "expandedRows": expanded["expandedRows"][:1]},
                    {**expanded, "expandedRows": [{**expanded["expandedRows"][0], "label": "Started foreign"}, expanded["expandedRows"][1]]},
                    {**expanded, "expandedRows": [{**expanded["expandedRows"][0], "inGroup": "false"}, expanded["expandedRows"][1]]}):
            with self.subTest(bad=bad), self.assertRaises(AssertionError):
                queue.started_group_ui_ok(bad, group, "expanded")
        boundary = {"kind": "item.completed", "event_id": "other", "seq": 10.5,
                    "payload": {"itemKind": "message", "text": "foreign work group"}}
        separated = queue.started_group([search, boundary, create, marker], "agt_child", "create")
        self.assertEqual([e["event_id"] for e in separated["entries"]], ["create"])
        with self.assertRaises(AssertionError):
            queue.started_group_ui_ok(expanded, separated, "expanded")
        with self.assertRaises(AssertionError):
            queue.started_group([search, create, marker], "agt_child", "search")
        with self.assertRaises(AssertionError):
            queue.started_group([search, create, marker, {**marker, "event_id": "foreign", "payload": {"child": "agt_foreign"}}], "agt_child", "create")

    def test_started_group_truncated_loom_fallback_rejects_non_loom_input(self):
        truncated = {"kind": "item.completed", "payload": {"itemKind": "tool", "tool": {
            "name": "execute", "input": "{'namespace':'loom', 'query':'agent_create'"}}}
        self.assertEqual(queue.started_create_role(truncated), "code")
        self.assertIsNone(queue.started_create_role({**truncated, "payload": {"itemKind": "tool", "tool": {
            "name": "execute", "input": "{'query':'agent_create'"}}}))

    def test_failed_create_tools_end_the_saved_group(self):
        inputs = (("loom.agent_create", json.dumps({"name": queue.CHILD_NAME})),
                  ("execute", json.dumps({"code": "await tools.loom.agent_create({name:'kid'})"})),
                  ("execute", "{'namespace':'loom', 'query':'agent_create'"))
        for name, raw in inputs:
            event = {"kind": "item.completed", "event_id": "failed", "payload": {"itemKind": "tool",
                     "tool": {"name": name, "input": raw, "failed": True}}}
            with self.subTest(name=name, raw=raw):
                self.assertIsNone(queue.started_create_role(event))
                self.assertTrue(queue.saved_chat_item(event))
        good = {"kind": "item.completed", "event_id": "create", "payload": {"itemKind": "tool",
                "tool": {"name": "loom.agent_create", "input": json.dumps({"name": queue.CHILD_NAME})}}}
        marker = {"kind": "child.created", "event_id": "created", "payload": {"child": "agt_child"}}
        with self.assertRaisesRegex(AssertionError, "exact native agent_create did not belong"):
            queue.started_group([good, event, marker], "agt_child", "create")

    def test_saved_chat_projection_boundaries_match_visible_turn_and_hidden_completion(self):
        good = {"kind": "item.completed", "event_id": "create", "payload": {"itemKind": "tool",
                "tool": {"name": "loom.agent_create", "input": json.dumps({"name": queue.CHILD_NAME})}}}
        marker = {"kind": "child.created", "event_id": "created", "payload": {"child": "agt_child"}}
        hidden = {"kind": "message.delivered", "event_id": "hidden", "payload": {
            "sender": "agent:agt_child", "completions": [{"child": "agt_child", "attempt": 0}], "message": ""}}
        self.assertFalse(queue.saved_chat_item(hidden))
        self.assertEqual(queue.started_group([good, hidden, marker], "agt_child", "create")["entries"],
                         [{"event_id": "create", "role": "create"}])
        failed_turn = {"kind": "agent.turn_completed", "event_id": "failed-turn", "payload": {"stopReason": "error"}}
        self.assertTrue(queue.saved_chat_item(failed_turn))
        with self.assertRaisesRegex(AssertionError, "exact native agent_create did not belong"):
            queue.started_group([good, failed_turn, marker], "agt_child", "create")
        self.assertFalse(queue.saved_chat_item({**failed_turn, "payload": {"stopReason": "completed"}}))

    def test_started_ui_scopes_expanded_rows_to_exact_marker_siblings(self):
        search = {"kind": "item.completed", "event_id": "search", "seq": 0,
                  "payload": {"itemKind": "tool", "tool": {"name": "execute", "input": json.dumps({
                      "code": "return await tools.loom.search({namespace:'loom',query:'agent_create'})"})}}}
        create = {"kind": "item.completed", "event_id": "create", "seq": 1,
                  "payload": {"itemKind": "tool", "tool": {"name": "loom.agent_create",
                      "input": json.dumps({"name": queue.CHILD_NAME})}}}
        marker = {"kind": "child.created", "event_id": "created", "seq": 2,
                  "payload": {"child": "agt_child", "name": queue.CHILD_NAME}}
        scripts = []
        def inspect_browser(expr):
            if expr == "location.pathname":
                return "/ws/LOCALMODE/chat/agt_lead"
            scripts.append(expr)
            return {"markerCount": 0, "ownCount": 0, "links": [], "toolCount": 0,
                    "expandedRows": [], "callText": "", "color": None}
        with patch.dict(os.environ, {"AFT_WS": "LOCALMODE"}), \
             patch.object(queue, "identity", side_effect=lambda label: "agt_lead" if label == "lead" else "agt_child"), \
             patch.object(queue, "events", return_value=[search, create, marker]), \
             patch.object(queue, "browser", side_effect=inspect_browser), patch.object(queue, "save"), \
             self.assertRaisesRegex(AssertionError, "Started child name/count/link mismatch"):
            queue.lead_ui("collapsed")
        self.assertEqual(len(scripts), 1)
        self.assertIn("nextElementSibling", scripts[0])
        self.assertIn("[data-testid=tool-call][data-in-group=true]", scripts[0])
        self.assertIn("span[class*=heading]", scripts[0])
        self.assertNotIn("querySelectorAll('[data-testid=bridge-call]')", scripts[0])
        parsed = subprocess.run(["node", "--input-type=module", "-e", "new Function('return '+process.argv[1])", scripts[0]],
                                capture_output=True, text=True)
        self.assertEqual(parsed.returncode, 0, parsed.stderr)
        common = Path(subprocess.check_output(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"],
                                              cwd=Path(__file__).resolve().parents[3], text=True).strip())
        jsdom = common.parent / "internal/webui/frontend/node_modules/jsdom/lib/api.js"
        self.assertTrue(jsdom.is_file(), f"installed frontend JSDOM needed for DOM oracle: {jsdom}")
        dom_code = """import {pathToFileURL} from 'node:url';
const {JSDOM}=await import(pathToFileURL(process.argv[2]).href);
globalThis.document=new JSDOM(process.argv[3]).window.document;
console.log(JSON.stringify(eval(process.argv[4])));"""
        marker_html = """<li data-kind="started"><div data-testid="started-marker">
          <a href="/ws/LOCALMODE/chat/agt_child">child</a><button aria-expanded="true">2 tool calls</button>
          </div></li>"""
        def row(label, in_group="true"):
            return f"""<li data-kind="work"><div data-testid="tool-call" data-in-group="{in_group}" data-status="completed">
              <div><span data-icon="other" aria-hidden="true">⚙</span><span class="heading_abc">{label}</span>
              <span class="chevron_abc" aria-hidden="true">⌄</span></div></div></li>"""
        def evaluated(extra=""):
            html = "<ul data-testid='chat-transcript'>" + marker_html + row("Ran code") + row("Started child") + extra + "</ul>"
            result = subprocess.run(["node", "--input-type=module", "-", str(jsdom), html, scripts[0]],
                                    input=dom_code, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        group = {"entries": [{"event_id": "search", "role": "code"}, {"event_id": "create", "role": "create"}]}
        positive = evaluated(row("Unrelated", "false"))
        self.assertEqual([r["label"] for r in positive["expandedRows"]], ["Ran code", "Started child"])
        queue.started_group_ui_ok(positive, group, "expanded")
        for extra in (row("Started foreign"), row("Ran code")):
            with self.subTest(extra=extra), self.assertRaisesRegex(AssertionError, "expanded Started rows differ"):
                queue.started_group_ui_ok(evaluated(extra), group, "expanded")

    def test_parsed_child_prompts_use_only_present_fixture_paths(self):
        fixture = Path(__file__).resolve().parents[2] / "fixtures/slack-clone"
        names = ("README.md", "BACKLOG.md", "package.json", "server.js", "app.js", "app.test.js")
        self.assertTrue(all((fixture / name).is_file() for name in names))
        self.assertEqual(json.loads((fixture / "package.json").read_text())["scripts"]["test"], "node --test")
        dockerfile = (Path(__file__).resolve().parents[3] / "test/local-mode/Dockerfile").read_text()
        self.assertIn("FROM local-mode AS agents\nCOPY --from=docker.io/library/node:24-bookworm-slim /usr/local/bin/node /usr/local/bin/node", dockerfile)
        prompts = parsed_queue_prompts()
        initial = next(p for p in prompts if "Brief it to inspect" in p)
        p2 = next(p for p in prompts if "QUEUE-P2-" in p)
        p3b = next(p for p in prompts if "QUEUE-P3B-" in p and "agent_send exactly once again" in p)
        self.assertTrue(all(name in initial and name in p2 for name in names))
        self.assertIn("node --test once", initial)
        self.assertIn(queue.TEXT["p2"].replace(queue.RUN, "${RUN_ID}"), p2)
        self.assertIn(queue.TEXT["p3b"].replace(queue.RUN, "${RUN_ID}"), p3b)
        for prompt in (initial, p2, p3b):
            self.assertNotRegex(prompt, r"docs/loom-glossary\.md|AGENTS\.md|internal/loomagent/|agentsv1|send\.go")

    def test_two_authentic_sender_slots_and_fifo_position(self):
        rows = [{"sender": "user:local", "text": "U2", "since": "t1"},
                {"sender": "agent:agt_lead", "text": "P1", "since": "t2"}]
        self.assertEqual(queue.sender_pair(rows, "U2", "P1", "agt_lead"), ("user:local", "agent:agt_lead"))
        for bad in (
            list(reversed(rows)),
            [{**rows[0], "sender": "agent:agt_lead"}, rows[1]],
            [rows[0], {**rows[1], "sender": "user:local"}],
            [rows[0], {**rows[1], "sender": "agent:agt_foreign"}],
        ):
            with self.assertRaises(AssertionError):
                queue.sender_pair(bad, "U2", "P1", "agt_lead")

    def test_delivery_requires_order_once_and_distinct_input_keys(self):
        rows = [delivery(10, "U2", "user:local", "in-u"),
                delivery(12, "P1", "agent:agt_lead", "in-p")]
        self.assertEqual(len(queue.delivery_pair(rows, "U2", "user:local", "P1", "agent:agt_lead", ("U1",))), 2)
        for bad in (
            [rows[1], rows[0]],
            [rows[0], {**rows[1], "seq": 9}],
            [rows[0], {**rows[1], "payload": {**rows[1]["payload"], "sender": "user:local"}}],
            [rows[0], {**rows[1], "payload": {**rows[1]["payload"], "inputKey": "in-u"}}],
            rows + [delivery(13, "P1", "agent:agt_lead", "in-duplicate")],
            rows + [delivery(14, "U1", "user:local", "in-superseded")],
        ):
            with self.assertRaises(AssertionError):
                queue.delivery_pair(bad, "U2", "user:local", "P1", "agent:agt_lead", ("U1",))

    def test_native_call_requires_completed_tool_and_exact_target(self):
        event = {"kind": "item.completed", "payload": {"itemKind": "tool", "tool": {
            "name": "execute", "input": {"code": 'await tools.loom.agent_send({agent:"agt_child",text:"QUEUE-P1"})'}}}}
        self.assertTrue(queue.actual_call(event, "agent_send", "agt_child", "QUEUE-P1"))
        for bad in (
            {**event, "kind": "item.started"},
            {"kind": "item.completed", "payload": {**event["payload"], "itemKind": "message"}},
            {"kind": "item.completed", "payload": {"itemKind": "tool", "tool": {
                "name": "execute", "input": {"code": 'await tools.loom.agent_get({agent:"agt_child"}) // QUEUE-P1'}}}},
        ):
            self.assertFalse(queue.actual_call(bad, "agent_send", "agt_child", "QUEUE-P1"))
        self.assertFalse(queue.actual_call(event, "agent_send", "agt_foreign", "QUEUE-P1"))

    def test_native_parent_replacement_receipt_must_be_true(self):
        first = {"event_id": "one", "turn_id": "turn-one", "kind": "item.completed", "payload": {
            "itemKind": "tool", "tool": {"output": '{"replaced":false,"message_id":"m1","state":"waiting"}'}}}
        second = {"event_id": "two", "turn_id": "turn-two", "kind": "item.completed", "payload": {
            "itemKind": "tool", "tool": {"output": '{"replaced":true,"message_id":"m2","state":"waiting"}'}}}
        self.assertEqual(queue.replacement_result(first, second)[1]["message_id"], "m2")
        for bad in ({**second, "payload": {"itemKind": "tool", "tool": {"output": '{"replaced":false,"message_id":"m2","state":"waiting"}'}}},
                    {**second, "payload": {"itemKind": "tool", "tool": {"output": '{}'}}},
                    {**second, "payload": {"itemKind": "tool", "tool": {"output": 'replaced:true'}}},
                    {**second, "payload": {"itemKind": "tool", "tool": {"output": '{"replaced":true,"message_id":"m1","state":"waiting"}'}}}):
            with self.assertRaises(AssertionError):
                queue.replacement_result(first, bad)
        with self.assertRaises(AssertionError):
            queue.replacement_result(first, {**second, "turn_id": "turn-one"})

    def test_native_send_ids_bind_exact_saved_waiting_request(self):
        child, sender = "agt_child", "agent:agt_lead"
        request = "agent_tool-" + "A" * 26
        message_id = "msg_" + hashlib.sha256(f"{child}\0{sender}\0{request}".encode()).digest()[:13].hex()
        wait = {"kind": "message.waiting", "agent_id": child,
                "event_id": f"{child}:send:{request}:message.waiting", "payload": {"reason": sender}}
        self.assertEqual(queue.native_wait_request(wait, child, sender, {"message_id": message_id}), request)
        for bad_wait, bad_result in (
            ({**wait, "event_id": wait["event_id"] + ":extra"}, {"message_id": message_id}),
            ({**wait, "event_id": f"agt_foreign:send:{request}:message.waiting"}, {"message_id": message_id}),
            ({**wait, "event_id": f"{child}:send:bad-request:message.waiting"}, {"message_id": message_id}),
            ({**wait, "agent_id": "agt_foreign"}, {"message_id": message_id}),
            ({**wait, "payload": {"reason": "user:local"}}, {"message_id": message_id}),
            (wait, {"message_id": "msg_" + "0" * 26}),
            (wait, {"message_id": ""}),
            (wait, {}),
        ):
            with self.subTest(bad_wait=bad_wait, bad_result=bad_result), self.assertRaises(AssertionError):
                queue.native_wait_request(bad_wait, child, sender, bad_result)

    def test_shot_rows_require_exact_waiting_count_order_and_first(self):
        u2, p1 = queue.TEXT["u2"], queue.TEXT["p1"]
        ui = {"waiting": ["Waiting " + u2, "Waiting from Lead " + p1], "history": [], "user": []}
        api = [{"text": u2, "sender": "user:local"}, {"text": p1, "sender": "agent:agt_lead"}]
        queue.assert_shot_rows("fifo-replaced", ui, api, "agt_lead")
        for bad_ui, bad_api in (({**ui, "waiting": list(reversed(ui["waiting"]))}, api),
                                (ui, list(reversed(api))),
                                ({**ui, "waiting": ui["waiting"][:1]}, api),
                                ({**ui, "history": [queue.TEXT["u1"]]}, api)):
            with self.assertRaises(AssertionError):
                queue.assert_shot_rows("fifo-replaced", bad_ui, bad_api, "agt_lead")
        first_ui = {"waiting": ["Waiting " + queue.TEXT["p3b"]],
                    "history": [queue.TEXT["u3"]], "user": [queue.TEXT["u3"]]}
        queue.assert_shot_rows("first-user-interrupt", first_ui, [{"text": queue.TEXT["p3b"], "sender": "agent:agt_lead"}], "agt_lead")
        with self.assertRaises(AssertionError):
            queue.assert_shot_rows("first-user-interrupt", {**first_ui, "user": []}, [{"text": queue.TEXT["p3b"], "sender": "agent:agt_lead"}], "agt_lead")
        queue.assert_shot_rows("first-parent-initial", {"waiting": [queue.TEXT["p3"]], "history": [], "user": []},
                               [{"text": queue.TEXT["p3"], "sender": "agent:agt_lead"}], "agt_lead")

    def test_owned_ref_probe_rejects_foreign_identity(self):
        with patch.dict(os.environ, {"AFT_TESTS_DIR": "/tmp"}), patch.object(queue.subprocess, "check_output", return_value=json.dumps({
            "agent_id": "agt_foreign", "kind": "child", "branch": "child", "head": "a" * 40})):
            with self.assertRaises(AssertionError):
                queue.native_ref("child", "agt_child", "b" * 40)

    def test_ref_shell_rejects_wrong_head_branch_and_container(self):
        with tempfile.TemporaryDirectory(prefix="queue-ref-oracle-") as temp:
            root = Path(temp)
            repo = root / "repo"
            repo.mkdir()
            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], text=True).strip()
            subprocess.run(["git", "init", "-q", "-b", "main", str(repo)], check=True)
            git("-c", "user.name=AFT", "-c", "user.email=aft@example.test", "commit", "-q", "--allow-empty", "-m", "base")
            base = git("rev-parse", "HEAD")
            git("switch", "-q", "-c", "child")
            git("-c", "user.name=AFT", "-c", "user.email=aft@example.test", "commit", "-q", "--allow-empty", "-m", "child")
            child_row = {"agent_id": "agt_child", "name": "cov-child-queue-task-af12345678", "preset": "task",
                         "created_by_kind": "agent", "created_by_id": "agt_lead", "parent_agent_id": "agt_lead",
                         "root_agent_id": "agt_lead", "repo": "source-repo", "harness": "opencode",
                         "worktree_path": str(repo), "branch": "child"}
            lead_row = {"agent_id": "agt_lead", "name": "cov-child-queue-lead-af12345678", "preset": "lead",
                        "parent_agent_id": None, "repo": "source-repo", "harness": "opencode"}
            child_file, lead_file = root / "child.json", root / "lead.json"
            def write_child():
                child_file.write_text(json.dumps(child_row))
            write_child()
            lead_file.write_text(json.dumps(lead_row))
            helper = root / "coverage-children-queue-ref.sh"
            helper.write_text(Path(__file__).with_name("coverage-children-queue-ref.sh").read_text())
            (root / "agent-flows-ownership.sh").write_text('''
agent_flows_check_manifest() { :; }
agent_flows_check_container() { [[ "$1" == owned-container ]]; }
curl() {
  local url="${@: -1}"
  case "$url" in */agt_child) cat "$CHILD_ROW";; */agt_lead) cat "$LEAD_ROW";; *) return 9;; esac
}
agent_flows_podman() {
  local arg
  for arg in "$@"; do
    if [[ "$arg" == ps ]]; then printf '%s\\n' "${STUB_CONTAINER:-owned-container}"; return; fi
  done
  while (($#)); do
    if [[ "$1" == exec ]]; then
      shift
      [[ "$1" == -T && "$2" == loom-local ]] || return 9
      shift 2
      "$@"
      return
    fi
    shift
  done
  return 9
}
''')
            env = {**os.environ, "RUN_ID": "af12345678", "AFT_API_URL": "http://127.0.0.1:1",
                   "AFT_AGENT_FLOW_REPO": "source-repo", "AFT_SOURCE_ROOT": str(root),
                   "AFT_OWNED_PROJECT": "owned-project", "AFT_WORK_DIR": str(root),
                   "CHILD_ROW": str(child_file), "LEAD_ROW": str(lead_file)}
            def probe(overrides=None):
                return subprocess.run(["bash", str(helper), "child", "agt_child", base],
                                      env={**env, **(overrides or {})}, capture_output=True, text=True)
            good = probe()
            self.assertEqual(good.returncode, 0, good.stderr)
            self.assertEqual(json.loads(good.stdout)["merge_base"], base)
            self.assertNotEqual(probe({"STUB_CONTAINER": "foreign-container"}).returncode, 0)
            child_row["branch"] = "wrong-branch"
            write_child()
            self.assertNotEqual(probe().returncode, 0)
            git("switch", "-q", "--orphan", "foreign")
            git("-c", "user.name=AFT", "-c", "user.email=aft@example.test", "commit", "-q", "--allow-empty", "-m", "unrelated")
            child_row["branch"] = "foreign"
            write_child()
            self.assertNotEqual(probe().returncode, 0)

    def test_screenshot_route_rejects_foreign_child(self):
        queue.assert_shot_route("/ws/LOCALMODE/chat/agt_child", "agt_child", "LOCALMODE")
        with self.assertRaises(AssertionError):
            queue.assert_shot_route("/ws/LOCALMODE/chat/agt_foreign", "agt_child", "LOCALMODE")

    def test_u3_exact_request_replay_rejects_new_event_or_changed_receipt(self):
        receipt = {"request_id": "request-u3", "body": {"text": queue.TEXT["u3"], "delivery": "interrupt"},
                   "result": {"message_id": "m3", "state": "waiting", "replaced": False, "interrupted": True}}
        proof = {"delivered": [{"input_key": "msg_" + "a" * 26}, {"input_key": "msg_" + "b" * 26}]}
        saved = {"receipt-u3": receipt, "first-delivery": proof}
        row = {"state": "finished", "attempt": 1, "waiting_messages": []}
        event = {"seq": 10, "event_id": "one"}
        with patch.object(queue, "load", side_effect=saved.__getitem__), patch.object(queue, "identity", return_value="agt_child"), \
             patch.object(queue, "agent", return_value=row), patch.object(queue, "events", side_effect=[[event], [event]]), \
             patch.object(queue, "http", return_value=receipt["result"]) as post, patch.object(queue, "save"):
            queue.replay_u3()
            self.assertEqual(post.call_args.args[2], receipt["request_id"])
        for replay, after in (({**receipt["result"], "replaced": True}, [event]),
                              (receipt["result"], [event, {"seq": 11, "event_id": "extra"}])):
            with patch.object(queue, "load", side_effect=saved.__getitem__), patch.object(queue, "identity", return_value="agt_child"), \
                 patch.object(queue, "agent", return_value=row), patch.object(queue, "events", side_effect=[[event], after]), \
                 patch.object(queue, "http", return_value=replay), patch.object(queue, "save"):
                with self.assertRaises(AssertionError):
                    queue.replay_u3()

    def test_native_input_probe_rejects_missing_or_duplicate_count(self):
        keys = ["msg_" + "a" * 26, "msg_" + "b" * 26]
        proof = {"delivered": [{"input_key": key} for key in keys]}
        def reply(args, text):
            key = args[-1]
            return json.dumps({"agent_id": "agt_child", "harness": "opencode", "input_key": key,
                               "native_id": "ses_child", "native_root": "/owned",
                               "native_user_message_count": 2 if key == keys[1] else 1})
        with patch.dict(os.environ, {"AFT_TESTS_DIR": "/tmp"}), patch.object(queue, "load", return_value=proof), \
             patch.object(queue, "identity", return_value="agt_child"), \
             patch.object(queue.subprocess, "check_output", side_effect=reply), patch.object(queue, "save"):
            with self.assertRaisesRegex(AssertionError, "exact input once"):
                queue.native_inputs()

    def test_native_probe_cli_rejects_compose_only_exec_flag(self):
        source = Path(__file__).with_name("coverage-children-queue-native.sh").read_text()
        line = next(x.strip() for x in source.splitlines() if x.startswith('agent_flows_podman exec '))
        prefix = line.split(' node -e ', 1)[0]
        check = ('agent_flows_podman() { [[ "$1" == exec && "$2" == owned && "$3" == /bin/true ]]; }; '
                 'container=owned; ' + prefix + ' /bin/true')
        self.assertEqual(subprocess.run(["bash", "-c", check], capture_output=True).returncode, 0)
        bad = check.replace(' exec "$container" ', ' exec -T "$container" ')
        self.assertNotEqual(subprocess.run(["bash", "-c", bad], capture_output=True).returncode, 0)

    def test_native_probe_accepts_empty_root_but_rejects_missing_root(self):
        source = Path(__file__).with_name("coverage-children-queue-native.sh").read_text()
        match = re.search(r'(const validNativeRow=\(row,run\)=>[\s\S]*?;)\n  if\(!validNativeRow', source)
        self.assertIsNotNone(match)
        script = match.group(1) + '''
const row={name:"cov-child-queue-task-af12345678",harness:"opencode",preset:"task",
 parent_agent_id:"agt_lead",root_agent_id:"agt_lead",created_by_id:"agt_lead",
 worktree_path:"/owned/child",native_id:"ses_child",native_root:""};
if(!validNativeRow(row,"af12345678") || validNativeRow({...row,native_root:null},"af12345678") ||
   validNativeRow({...row,created_by_id:"agt_foreign"},"af12345678")) process.exit(1);
'''
        result = subprocess.run(["node", "-e", script], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_event_pager_rejects_foreign_child_and_duplicate_event(self):
        expected = {"agent_id": "agt_child", "seq": 1, "event_id": "one", "kind": "message.waiting", "payload": {}}
        with patch.object(queue, "identity", return_value="agt_child"), patch.object(queue, "http", return_value={
            "snapshot_seq": 1, "events": [{**expected, "agent_id": "agt_foreign"}], "more": False}):
            with self.assertRaises(AssertionError):
                queue.events("child")
        with patch.object(queue, "identity", return_value="agt_child"), patch.object(queue, "http", return_value={
            "snapshot_seq": 2, "events": [expected, {**expected, "seq": 2}], "more": False}):
            with self.assertRaises(AssertionError):
                queue.events("child")


if __name__ == "__main__":
    unittest.main()
