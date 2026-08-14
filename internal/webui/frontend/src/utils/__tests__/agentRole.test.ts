import { describe, expect, it } from "vitest";

import type { LoomAgentStatus } from "@/types";

import {
  agentRailRank,
  buildEpicLeadClaims,
  detectTeamTemplate,
  findTemplateArchitectAgentName,
  isBackgroundAgent,
  isInteractiveAgent,
  isLeadRole,
  isWorkerRole,
  splitAgentsByRuntime,
} from "../agentRole";
import type { TeamTemplateApplyReport } from "@/types/teamTemplate";

function makeAgent(
  overrides: Partial<LoomAgentStatus> & Pick<LoomAgentStatus, "name">,
): LoomAgentStatus {
  return {
    branch: "",
    status: "idle",
    ahead: 0,
    behind: 0,
    workspace: "default",
    ...overrides,
  };
}

describe("isLeadRole", () => {
  it("matches lead and orchestrator roles", () => {
    expect(isLeadRole("lead")).toBe(true);
    expect(isLeadRole("orchestrator")).toBe(true);
    expect(isLeadRole("task")).toBe(false);
  });
});

describe("isWorkerRole", () => {
  it("matches plan and task worker roles", () => {
    expect(isWorkerRole("plan")).toBe(true);
    expect(isWorkerRole("planner")).toBe(true);
    expect(isWorkerRole("task")).toBe(true);
    expect(isWorkerRole("lead")).toBe(false);
  });
});

describe("isInteractiveAgent", () => {
  it("uses role_kind when present", () => {
    expect(
      isInteractiveAgent(
        makeAgent({
          name: "operator-a",
          role: "operator",
          role_kind: "interactive",
        }),
      ),
    ).toBe(true);
    expect(
      isInteractiveAgent(
        makeAgent({ name: "lead-a", role: "lead", role_kind: "worker" }),
      ),
    ).toBe(false);
  });

  it("falls back to lead role names when role_kind is absent", () => {
    expect(
      isInteractiveAgent(makeAgent({ name: "lead-a", role: "lead" })),
    ).toBe(true);
    expect(
      isInteractiveAgent(makeAgent({ name: "task-a", role: "task" })),
    ).toBe(false);
  });
});

describe("isBackgroundAgent", () => {
  it("treats lead agents as regular even when daemon-managed", () => {
    expect(
      isBackgroundAgent(
        makeAgent({ name: "lead-a", role: "lead", daemon_managed: true }),
      ),
    ).toBe(false);
  });

  it("treats daemon-managed workers as background", () => {
    expect(
      isBackgroundAgent(
        makeAgent({ name: "task-a", role: "task", daemon_managed: true }),
      ),
    ).toBe(true);
  });

  it("treats plan/task roles as background without daemon flag", () => {
    expect(
      isBackgroundAgent(makeAgent({ name: "planner-a", role: "plan" })),
    ).toBe(true);
    expect(isBackgroundAgent(makeAgent({ name: "task-a", role: "task" }))).toBe(
      true,
    );
  });

  it("treats interactive-kind custom agents as regular", () => {
    expect(
      isBackgroundAgent(
        makeAgent({
          name: "operator-a",
          role: "operator",
          role_kind: "interactive",
          daemon_managed: true,
        }),
      ),
    ).toBe(false);
  });
});

describe("splitAgentsByRuntime", () => {
  it("separates lead from supervised workers", () => {
    const lead = makeAgent({ name: "lead-a", role: "lead" });
    const planner = makeAgent({ name: "planner-a", role: "plan" });
    const task = makeAgent({ name: "task-a", role: "task" });

    expect(splitAgentsByRuntime([planner, lead, task])).toEqual({
      regular: [lead],
      background: [planner, task],
    });
  });
});

describe("agentRailRank", () => {
  it("ranks interactive-kind agents first", () => {
    expect(
      agentRailRank(
        makeAgent({
          name: "operator-a",
          role: "operator",
          role_kind: "interactive",
        }),
      ),
    ).toBe(0);
    expect(agentRailRank(makeAgent({ name: "worker-a", role: "task" }))).toBe(
      2,
    );
  });
});

describe("buildEpicLeadClaims", () => {
  it("claims epics only for lead-capable role names", () => {
    const claims = buildEpicLeadClaims([
      makeAgent({
        name: "operator-a",
        role: "operator",
        role_kind: "interactive",
        parent: "EPIC-1",
      }),
      makeAgent({ name: "lead-a", role: "lead", parent: "EPIC-2" }),
      makeAgent({ name: "task-a", role: "task", parent: "EPIC-3" }),
    ]);

    expect(claims.has("EPIC-1")).toBe(false);
    expect(claims.get("EPIC-2")).toBe("lead-a");
    expect(claims.has("EPIC-3")).toBe(false);
  });
});

describe("detectTeamTemplate", () => {
  it("matches a bundle only when every configured worker agent role is present", () => {
    expect(
      detectTeamTemplate({
        roleNames: [
          "plan",
          "task",
          "lead",
          "app-architect",
          "frontend-dev",
          "backend-dev",
          "qa-engineer",
        ],
      })?.id,
    ).toBe("fullstack-app");

    expect(
      detectTeamTemplate({
        roleNames: ["app-architect", "frontend-dev", "backend-dev"],
      }),
    ).toBeNull();
  });

  it("treats an in-session apply report as authoritative", () => {
    const report = {
      template_id: "website",
      revision: 1,
    } as TeamTemplateApplyReport;

    expect(detectTeamTemplate({ roleNames: [], applyReport: report })?.id).toBe(
      "website",
    );
  });

  it("uses the local breadcrumb only to break a tie between live matches", () => {
    const combinedRoles = [
      "app-architect",
      "frontend-dev",
      "backend-dev",
      "qa-engineer",
      "api-architect",
      "data-engineer",
    ];

    expect(
      detectTeamTemplate({
        roleNames: combinedRoles,
        breadcrumb: {
          templateId: "backend",
          revision: 1,
          ts: 1,
          counts: { created: 9, skipped: 0, diverged: 0, failed: 0 },
        },
      })?.id,
    ).toBe("backend");

    expect(
      detectTeamTemplate({
        roleNames: [],
        breadcrumb: {
          templateId: "backend",
          revision: 1,
          ts: 1,
          counts: { created: 9, skipped: 0, diverged: 0, failed: 0 },
        },
      }),
    ).toBeNull();
  });
});

describe("findTemplateArchitectAgentName", () => {
  it("finds the configured architect from live agent role names", () => {
    expect(
      findTemplateArchitectAgentName({
        teamTemplateId: "fullstack-app",
        agents: [
          { name: "frontend-dev-1", role_name: "frontend-dev" },
          { name: "app-architect-1", role_name: "app-architect" },
        ],
      }),
    ).toBe("app-architect-1");
  });

  it("accepts a non-failed architect-agent step from the in-session report", () => {
    expect(
      findTemplateArchitectAgentName({
        teamTemplateId: "backend",
        agents: [],
        applyReport: {
          template_id: "backend",
          revision: 1,
          schema_version: 1,
          workspace_key: "ws",
          dry_run: false,
          steps: [
            { entity: "agent", name: "api-architect-1", action: "created" },
          ],
          created: 1,
          skipped: 0,
          diverged: 0,
          failed: 0,
          materialized: 1,
        },
      }),
    ).toBe("api-architect-1");
  });
});
