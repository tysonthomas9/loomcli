import { describe, expect, it } from "vitest";

import type { AgentEvent } from "@/api/agentsv1";
import type { ChatItem } from "@/hooks";
import {
  STEP_MAX,
  bridgeLabel,
  deriveTimelineRows,
  stepLabel,
  summarizeToolGroup,
  toolHeading,
  toolPreview,
} from "../timelineRows";

const tool = (
  key: string,
  name: string,
  status: "running" | "completed" | "failed" = "completed",
): Extract<ChatItem, { kind: "tool" }> => ({
  key,
  kind: "tool",
  tool: { name },
  status,
});
const text = (key: string): ChatItem => ({ key, kind: "agent", text: key });
const thought = (key: string): ChatItem => ({
  key,
  kind: "reasoning",
  text: key,
});

describe("timelineRows", () => {
  it("marks a group failed when any call failed, not only the last", () => {
    const [summary] = deriveTimelineRows(
      [tool("1", "read"), tool("2", "read", "failed"), tool("3", "bash")],
      new Set(),
    );
    expect(summary).toMatchObject({ kind: "work-toggle", hasFailure: true });
    const rows = deriveTimelineRows(
      [tool("1", "bash", "failed"), thought("t"), tool("2", "read")],
      new Set(),
    );
    expect(rows.find((r) => r.kind === "work-toggle")).toMatchObject({
      hiddenCount: 2,
      hasFailure: true,
    });
  });

  it("summarizes a tool group by what the calls did", () => {
    expect(
      summarizeToolGroup([
        tool("1", "bash"),
        tool("2", "Bash"),
        tool("3", "read"),
      ]),
    ).toBe("Ran 2 commands and read 1 file");
    expect(
      summarizeToolGroup([
        tool("1", "edit"),
        tool("2", "grep"),
        tool("3", "x"),
      ]),
    ).toBe("Changed 1 file, searched code 1 time, and used 1 tool");
    expect(summarizeToolGroup([tool("1", "loom/agent_list")])).toBe(
      "Used 1 tool",
    );
  });

  it("collapses consecutive tool calls into one summary row until expanded", () => {
    const items = [
      text("a"),
      tool("t1", "bash"),
      tool("t2", "read", "failed"),
      text("b"),
    ];
    const rows = deriveTimelineRows(items, new Set());
    expect(rows.map((r) => r.kind)).toEqual(["item", "work-toggle", "item"]);
    const toggle = rows[1] as Extract<
      (typeof rows)[number],
      { kind: "work-toggle" }
    >;
    expect(toggle).toMatchObject({
      count: 2,
      summary: "Ran 1 command and read 1 file",
      hasFailure: true,
      expanded: false,
    });
    const open = deriveTimelineRows(items, new Set([toggle.groupId]));
    expect(open.map((r) => r.kind)).toEqual([
      "item",
      "work-toggle",
      "work",
      "work",
      "item",
    ]);
  });

  it("shows a running group as one live row naming the running call", () => {
    const rows = deriveTimelineRows(
      [tool("t1", "bash"), tool("t2", "read", "running")],
      new Set(),
    );
    expect(rows).toEqual([
      expect.objectContaining({
        kind: "work-live",
        entry: expect.objectContaining({ key: "t2" }),
      }),
    ]);
  });

  it("shows a mixed group's last entry after Show N earlier steps", () => {
    const rows = deriveTimelineRows(
      [thought("r1"), tool("t1", "bash"), thought("r2")],
      new Set(),
    );
    expect(rows.map((r) => [r.kind, r.id])).toEqual([
      ["work", "r2"],
      ["work-toggle", "work-toggle:r1"],
    ]);
    expect(rows[1]).toMatchObject({ hiddenCount: 2, onlyToolEntries: false });
  });

  it("shows live reasoning as one Thinking row at the end", () => {
    const rows = deriveTimelineRows(
      [
        text("a"),
        { key: "live:r", kind: "reasoning", text: "x", streaming: true },
      ],
      new Set(),
    );
    expect(rows.map((r) => r.kind)).toEqual(["item", "thinking"]);
  });
});

describe("stepLabel", () => {
  const ev = (kind: string, payload: object): AgentEvent => ({
    agent_id: "c",
    seq: 0,
    event_id: "e",
    kind,
    turn_id: "t",
    payload,
    created_at: "",
  });
  const started = (name: string, input?: string) =>
    ev("tool.started", {
      itemKind: "tool",
      tool: input === undefined ? { name } : { name, input },
    });

  it("names a tool by its action and salient argument on every harness", () => {
    for (const name of ["bash", "Bash", "exec_command", "shell"])
      expect(stepLabel(started(name, '{"command":"npm test"}'))).toBe(
        "▸ Ran command · npm test",
      );
    expect(stepLabel(started("read", '{"filePath":"src/a.ts"}'))).toBe(
      "▸ Read file · src/a.ts",
    );
    expect(
      stepLabel(
        ev("item.completed", {
          itemKind: "tool",
          tool: { name: "grep", input: '{"pattern":"TODO"}', output: "x" },
        }),
      ),
    ).toBe("▸ Searched code · TODO");
    expect(stepLabel(started("todowrite"))).toBe("▸ Todowrite");
  });

  it("never shows raw tool input", () => {
    expect(stepLabel(started("todowrite", '{"todos":[{"id":1}]}'))).toBe(
      "▸ Todowrite",
    );
    expect(stepLabel(started("bash", '{"command":"ls"}'), false)).toBe(
      "▸ Ran command",
    );
  });

  it("masks credentials and never shows unstructured input", () => {
    const bearer = stepLabel(
      started(
        "bash",
        JSON.stringify({
          command:
            "curl -H 'Authorization: Bearer sk-example' https://example.com",
        }),
      ),
    )!;
    expect(bearer).toContain("▸ Ran command · curl -H 'Authorization");
    expect(bearer).not.toMatch(/sk-example|sk-exa/);
    for (const command of [
      "SECRET=hunter2 npm test",
      "export GITHUB_TOKEN=ghp_abcdefghijklmnop",
      "mysql --password=hunter2 -u root",
      "git clone https://bob:hunter2@example.com/r.git",
      "echo sk-proj-abcdefghijkl",
    ]) {
      const got = stepLabel(started("bash", JSON.stringify({ command })))!;
      expect(got).not.toMatch(/hunter2|ghp_abc|sk-proj/);
      expect(got).toContain("•••");
    }
    expect(stepLabel(started("bash", "SECRET=hunter2 npm test"))).toBe(
      "▸ Ran command",
    );
    expect(stepLabel(started("bash", "npm test"))).toBe("▸ Ran command");
  });

  it("cuts a long input to about 60 characters", () => {
    const long = `npm test -- ${"x".repeat(200)}`;
    const got = stepLabel(started("bash", JSON.stringify({ command: long })))!;
    expect(got.length).toBeLessThanOrEqual(STEP_MAX);
    expect(got.startsWith("▸ Ran command · npm test -- x")).toBe(true);
    expect(got.endsWith("…")).toBe(true);
  });

  it("shows reasoning's first plain line", () => {
    expect(
      stepLabel(
        ev("item.completed", {
          itemKind: "reasoning",
          text: "**Checking routes**\n\nThe router lives in app.ts.",
        }),
      ),
    ).toBe("💭 Thinking · Checking routes");
    expect(
      stepLabel(ev("item.completed", { itemKind: "reasoning", text: "" })),
    ).toBe("💭 Thinking");
  });

  it("is null for anything but a step", () => {
    expect(
      stepLabel(ev("item.completed", { itemKind: "message", text: "hi" })),
    ).toBeNull();
    expect(stepLabel(ev("delta", { itemKind: "message", text: "hi" }))).toBe(
      null,
    );
  });
});

const exec = (
  key: string,
  code: string,
  status: "running" | "completed" | "failed" = "completed",
): Extract<ChatItem, { kind: "tool" }> => ({
  key,
  kind: "tool",
  tool: { name: "execute", input: JSON.stringify({ code }) },
  status,
});
const create = (key: string, status?: "completed" | "failed") =>
  exec(
    key,
    "return await tools.loom.agent_create({brief:'run tests'})",
    status,
  );
const startedItem = (key: string, ...ids: string[]): ChatItem => ({
  key,
  kind: "started",
  children: ids.map((child) => ({ child, name: `ui-${child}` })),
  at: "",
});

describe("Loom bridge calls (CL1)", () => {
  const names = new Map([["agt_1", "ui-test-agent-1"]]);

  it("labels agent_get as Checked <name>, never its raw input", () => {
    const get = exec("g", "return await tools.loom.agent_get({agent:'agt_1'})");
    expect(bridgeLabel(get, names)).toBe("Checked ui-test-agent-1");
    expect(bridgeLabel({ ...get, status: "running" }, names)).toBe(
      "Checking ui-test-agent-1",
    );
    // An id with no known name shows as the id.
    expect(
      bridgeLabel(
        exec("g2", 'tools.loom.agent_get({ agent: "agt_9" })'),
        names,
      ),
    ).toBe("Checked agt_9");
  });

  it("labels bridge tools called by name, as Claude and Codex call them", () => {
    const named = (name: string, input: object) => ({
      ...tool("n", name),
      tool: { name, input: JSON.stringify(input) },
    });
    expect(
      bridgeLabel(named("mcp__loom__agent_get", { agent: "agt_1" }), names),
    ).toBe("Checked ui-test-agent-1");
    expect(bridgeLabel(named("agent_list", {}), names)).toBe("Listed agents");
    expect(
      bridgeLabel(
        named("loom/agent_send", { agent: "agt_1", text: "hi" }),
        names,
      ),
    ).toBe("Messaged ui-test-agent-1");
    expect(bridgeLabel(named("agent_archive", { agent: "agt_1" }), names)).toBe(
      "Archived ui-test-agent-1",
    );
    expect(bridgeLabel(named("github_read", {}), names)).toBe("Read GitHub");
  });

  it("counts several calls in one execute", () => {
    expect(
      bridgeLabel(
        exec(
          "m",
          "const a = await tools.loom.agent_get({agent:'agt_1'}); return tools.loom.agent_list({})",
        ),
        names,
      ),
    ).toBe("Checked ui-test-agent-1 and 1 more call");
  });

  it("leaves other tools alone", () => {
    expect(bridgeLabel(tool("b", "bash"), names)).toBeNull();
    expect(bridgeLabel(exec("e", "return 1 + 1"), names)).toBeNull();
    expect(bridgeLabel(tool("x", "agent_getter"), names)).toBeNull();
  });

  it("shows a bridge call as its own muted row, not in a work group", () => {
    const rows = deriveTimelineRows(
      [
        startedItem("s", "agt_1"),
        text("m"),
        thought("t"),
        exec("g", "return await tools.loom.agent_get({agent:'agt_1'})"),
      ],
      new Set(),
    );
    expect(rows.map((r) => r.kind)).toEqual([
      "started",
      "item",
      "work",
      "bridge",
    ]);
    expect(rows[3]).toMatchObject({ label: "Checked ui-agt_1" });
  });

  it("folds the agent_create calls after a Started marker into it", () => {
    const rows = deriveTimelineRows(
      [
        startedItem("s1", "k1"),
        startedItem("s2", "k2"),
        create("c1"),
        create("c2"),
      ],
      new Set(),
    );
    // Back-to-back markers merge in chatItems; here one per item, then folded.
    expect(rows.some((r) => r.kind === "work-toggle")).toBe(false);
    expect(rows.filter((r) => r.kind === "started")).toHaveLength(1);
    const [s] = rows;
    expect(s).toMatchObject({ kind: "started", expanded: false });
    if (s?.kind !== "started") throw new Error("no marker");
    expect(s.item.children.map((c) => c.child)).toEqual(["k1", "k2"]);
    expect(s.calls.map((c) => c.key)).toEqual(["c1", "c2"]);
  });

  it("folds create calls before a marker, and merges markers only they keep apart", () => {
    const rows = deriveTimelineRows(
      [
        create("c1"),
        startedItem("s1", "k1"),
        create("c2"),
        startedItem("s2", "k2"),
      ],
      new Set(),
    );
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ kind: "started", id: "s1" });
    if (rows[0]?.kind !== "started") throw new Error("no marker");
    expect(rows[0].calls.map((c) => c.key)).toEqual(["c1", "c2"]);
    expect(rows[0].item.children.map((c) => c.child)).toEqual(["k1", "k2"]);
  });

  it("expands the folded calls as labelled work rows", () => {
    const rows = deriveTimelineRows(
      [startedItem("s1", "k1"), create("c1")],
      new Set(["started:s1"]),
    );
    expect(rows.map((r) => r.kind)).toEqual(["started", "work"]);
    expect(rows[1]).toMatchObject({
      inGroup: true,
      label: "Started an agent",
    });
  });

  it("keeps a failed or running create call out of the marker", () => {
    const rows = deriveTimelineRows(
      [startedItem("s1", "k1"), create("c1", "failed")],
      new Set(),
    );
    expect(rows.map((r) => r.kind)).toEqual(["started", "bridge"]);
    if (rows[0]?.kind !== "started") throw new Error("no marker");
    expect(rows[0].calls).toEqual([]);
    const live = deriveTimelineRows(
      [
        startedItem("s1", "k1"),
        { ...create("c2"), status: "running" as const },
      ],
      new Set(),
    );
    expect(live[1]).toMatchObject({
      kind: "bridge",
      label: "Starting an agent",
    });
  });
});

describe("code mode headers (CL4)", () => {
  const raw = (key: string, name: string, input: string) => ({
    ...tool(key, name),
    tool: { name, input },
  });
  const names = new Map<string, string>();

  const search = "const s=search({namespace:'loom', query:'agent_create'})";

  it("never previews an execute's input, even cut short with code not first", () => {
    const cut = raw(
      "c",
      "execute",
      `{"timeout":5,"code":"${search}; await s[0].ca`,
    );
    expect(toolHeading(cut)).toBe("Ran code");
    expect(toolPreview(cut)).toBe("");
  });

  it("folds loom code naming agent_create into the Started marker next to it", () => {
    const mixed = exec(
      "m",
      `await tools.loom.agent_list({}); ${search}; await s[0].call({})`,
    );
    const rows = deriveTimelineRows(
      [
        startedItem("s", "k1"),
        exec("c", `${search}; await s[0].call({})`),
        mixed,
      ],
      new Set(),
    );
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({
      kind: "started",
      calls: [{ key: "c" }, { key: "m" }],
    });
  });

  it("folds loom code cut short before its agent_create into the marker next to it", () => {
    const cut = raw(
      "c",
      "execute",
      `{"code":"const s=search({namespace:'loom', query:'agents'}); /* 16 KiB…`,
    );
    const rows = deriveTimelineRows([startedItem("s", "k1"), cut], new Set());
    expect(rows).toEqual([
      expect.objectContaining({ kind: "started", calls: [cut] }),
    ]);
  });

  it("never folds cut loom code that calls another bridge tool", () => {
    const cut = raw(
      "l",
      "execute",
      `{"code":"await tools.loom.agent_list({}); /* 16 KiB…`,
    );
    const rows = deriveTimelineRows([startedItem("s", "k1"), cut], new Set());
    expect(rows[0]).toMatchObject({ kind: "started", calls: [] });
  });

  it("shows such code with no marker next to it as a plain expandable row", () => {
    const only = exec("o", search);
    expect(bridgeLabel(only, names)).toBeNull();
    const rows = deriveTimelineRows(
      [text("t"), only],
      new Set(["work-group:o"]),
    );
    expect(rows.map((r) => r.kind)).toEqual(["item", "work-toggle", "work"]);
    // Code outside the loom namespace never folds.
    const other = exec("x", "// agent_create is not called here\nreturn 1");
    const next = deriveTimelineRows(
      [startedItem("s", "k1"), create("c1"), other],
      new Set(),
    );
    expect(next[0]).toMatchObject({ kind: "started", calls: [{ key: "c1" }] });
  });

  it("leaves the heading and preview of other tools with a code argument alone", () => {
    const py = raw("p", "python", JSON.stringify({ code: "print(1)" }));
    expect(toolHeading(py)).toBe("Python");
    expect(toolPreview(py)).not.toBe("");
  });

  it("shows a finished execute in the tray as Ran code", () => {
    const e = (kind: string): AgentEvent => ({
      agent_id: "c",
      seq: 0,
      event_id: "e",
      kind,
      turn_id: "t",
      payload: {
        itemKind: "tool",
        tool: { name: "execute", input: JSON.stringify({ code: "return 1" }) },
      },
      created_at: "",
    });
    expect(stepLabel(e("tool.started"))).toBe("▸ Running code");
    expect(stepLabel(e("item.completed"))).toBe("▸ Ran code");
  });
});
