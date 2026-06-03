import { describe, expect, it } from "vitest";
import { bootstrapFromEnv } from "../bootstrap.js";

describe("bootstrapFromEnv", () => {
  const base = {
    LOOM_SERVER_URL: "https://loom.example.com",
    LOOM_WORKSPACE: "DEMO",
    LOOM_TASK_ID: "DEMO-1",
  };

  it("parses a full bootstrap", () => {
    const b = bootstrapFromEnv({
      ...base,
      LOOM_SESSION_ID: "sess_1",
      LOOM_FENCING_TOKEN: "7",
      LOOM_TASKRUN_TOKEN: "tok",
      LOOM_FLEET_DB_ACTOR: "loom-dev",
    });
    expect(b).toEqual({
      serverUrl: "https://loom.example.com",
      workspace: "DEMO",
      taskId: "DEMO-1",
      sessionId: "sess_1",
      fencingToken: "7",
      token: "tok",
      actor: "loom-dev",
    });
  });

  it("falls back to LOOM_ASSIGNED_TASK_ID", () => {
    const b = bootstrapFromEnv({
      LOOM_SERVER_URL: base.LOOM_SERVER_URL,
      LOOM_WORKSPACE: base.LOOM_WORKSPACE,
      LOOM_ASSIGNED_TASK_ID: "DEMO-9",
    });
    expect(b.taskId).toBe("DEMO-9");
  });

  it("requires server url, workspace, and a task id", () => {
    expect(() => bootstrapFromEnv({})).toThrow(/LOOM_SERVER_URL/);
    expect(() => bootstrapFromEnv({ LOOM_SERVER_URL: "x" })).toThrow(/LOOM_WORKSPACE/);
    expect(() =>
      bootstrapFromEnv({ LOOM_SERVER_URL: "x", LOOM_WORKSPACE: "w" }),
    ).toThrow(/LOOM_TASK_ID/);
  });
});
