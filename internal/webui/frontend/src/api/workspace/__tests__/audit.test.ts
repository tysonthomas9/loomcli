import { beforeEach, describe, expect, it, vi } from "vitest";

import { get } from "@/api/common";
import { fetchAuditEvents } from "../audit";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return { ...actual, get: vi.fn() };
});

const mockGet = vi.mocked(get);

describe("fetchAuditEvents", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("requests and unwraps the locked workspace audit envelope", async () => {
    const page = {
      events: [
        {
          cursor: "100-0",
          timestamp: "2026-08-14T18:00:00Z",
          actor: "api-architect-1",
          action: "issue.claim",
          entity_type: "issue",
          entity_id: "TEAMBACKEND-1",
          details: {},
        },
      ],
      next_cursor: "99-0",
    };
    mockGet.mockResolvedValue({ success: true, data: page });

    await expect(
      fetchAuditEvents("team/ws", {
        since: "101-0",
        limit: 25,
        entity: "TEAMBACKEND-1",
        actor: "api-architect-1",
      }),
    ).resolves.toEqual(page);

    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/team%2Fws/audit?since=101-0&limit=25&entity=TEAMBACKEND-1&actor=api-architect-1",
    );
  });

  it("omits unset filters and uses the default page size", async () => {
    mockGet.mockResolvedValue({
      success: true,
      data: { events: [], next_cursor: "" },
    });

    await fetchAuditEvents("default");

    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/default/audit?limit=50",
    );
  });

  it("rejects an unsuccessful response envelope", async () => {
    mockGet.mockResolvedValue({ success: false, error: "audit unavailable" });

    await expect(fetchAuditEvents("default")).rejects.toThrow();
  });
});
