import { describe, expect, it } from "vitest";

import type { AgentServiceDTO } from "@/api/agentServices";
import type { LoomAgentStatus } from "@/types";

import {
  buildAgentAutomationRows,
  withoutDurableAgentProjections,
} from "../agentSectionAutomationRows";

function service(id: string, name: string): AgentServiceDTO {
  return {
    id,
    name,
    kind: "scripted",
    enabled: true,
    behavior: { driverId: id, driverVersionId: "v1" },
    bindings: [
      {
        id: `${id}-weekly`,
        sourceKind: "cron",
        schedule: "@weekly",
        enabled: true,
        routeKey: `cron.${id}.weekly`,
      },
    ],
    nextFireAt: null,
    lastRunStatus: "",
    consecutiveFailures: 0,
    errors: [],
    createdAt: "2026-08-14T00:00:00Z",
    updatedAt: "2026-08-14T00:00:00Z",
  };
}

describe("agent-section autonomous rows", () => {
  it("constructs each row from the DTO's embedded bindings", () => {
    const scout = service("scout", "Scout");

    expect(buildAgentAutomationRows([scout])).toEqual({
      durableRecords: [
        {
          id: "scout",
          record: scout,
          bindings: scout.bindings,
        },
      ],
    });
  });

  it("removes roster projections matching durable record ids or names", () => {
    const agents = [
      { name: "lead" },
      { name: "scout" },
      { name: "Scout" },
      { name: "coder" },
    ] as LoomAgentStatus[];

    expect(
      withoutDurableAgentProjections(agents, [service("scout", "Scout")]).map(
        (agent) => agent.name,
      ),
    ).toEqual(["lead", "coder"]);
  });
});
