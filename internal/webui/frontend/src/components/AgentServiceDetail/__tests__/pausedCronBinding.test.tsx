// @vitest-environment jsdom

import {
  fireEvent,
  render as testingLibraryRender,
  screen,
  waitFor,
} from "@testing-library/react";
import type { ReactElement } from "react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentServiceDTO } from "@/api/agentServices";
import { KeyboardShortcutProvider } from "@/hooks/ui";

import { AgentServiceDetail } from "../AgentServiceDetail";

function render(ui: ReactElement) {
  return testingLibraryRender(
    <KeyboardShortcutProvider>{ui}</KeyboardShortcutProvider>,
  );
}

const mocks = vi.hoisted(() => ({
  patchAgentService: vi.fn(),
  removeAgentService: vi.fn(),
  noop: vi.fn(),
}));

vi.mock("@/hooks/workspace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks/workspace")>();
  return {
    ...actual,
    useAgentServiceRuns: () => ({
      runs: [],
      total: 0,
      loading: false,
      initialized: true,
      error: null,
      notFound: false,
      refresh: mocks.noop,
    }),
    useAgentServiceMutations: () => ({
      create: vi.fn(),
      patch: mocks.patchAgentService,
      remove: mocks.removeAgentService,
    }),
  };
});

vi.mock("@/api/agentServices", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/agentServices")>();
  return {
    ...actual,
    listRunEvents: vi.fn().mockResolvedValue({ events: [] }),
    getAgentServiceJournal: vi.fn().mockResolvedValue({
      serviceId: "scout",
      filename: "history.md",
      content: "",
      modifiedAt: "2026-08-20T12:00:00Z",
      truncated: false,
    }),
    listAgentServiceRunTasks: vi.fn().mockResolvedValue({ data: [], total: 0 }),
  };
});

vi.mock("@/components/RolePromptCard", () => ({
  RolePromptCard: () => <div data-testid="role-prompt-card" />,
}));

// The state that stranded a real operator: the service itself is enabled, but its
// cron binding was disabled out-of-band, so the schedule will never fire.
const pausedByBinding: AgentServiceDTO = {
  id: "scout",
  name: "Scout",
  triggerKind: "cron",
  enabled: true,
  behavior: {
    roleName: "scout",
    roleDisplayName: "Scout",
    workflowName: "scout",
    scripted: true,
  },
  bindings: [
    {
      id: "binding-cron-scout-weekly",
      sourceKind: "cron",
      schedule: "@weekly",
      timezone: "America/Los_Angeles",
      enabled: false,
      routeKey: "cron.scout.weekly",
      updatedBy: "alice@example.com",
    },
  ],
  nextFireAt: null,
  lastRunStatus: "completed",
  consecutiveFailures: 0,
  errors: [],
  createdAt: "2026-08-19T17:52:51Z",
  updatedAt: "2026-08-20T21:09:24Z",
};

describe("a cron binding disabled out-of-band", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.patchAgentService.mockImplementation(async () => pausedByBinding);
  });

  // B9: the label must name the flag that is actually holding the schedule.
  it("reports the schedule as paused rather than merely unscheduled", () => {
    render(<AgentServiceDetail workspaceId="WS" service={pausedByBinding} />);
    const nextFire = screen.getByText("Next fire").nextElementSibling;
    expect(nextFire).toHaveTextContent("Paused");
    expect(nextFire).not.toHaveTextContent("Not scheduled");
  });

  // B10: the actor behind the last edit must be visible, so a changed schedule
  // or a paused binding does not require auditing source to attribute.
  it("names the actor behind the binding's last change", () => {
    render(<AgentServiceDetail workspaceId="WS" service={pausedByBinding} />);
    expect(
      screen.getByTestId("binding-updated-by-binding-cron-scout-weekly"),
    ).toHaveTextContent("Last changed by alice@example.com");
  });

  // B11: the operator must be able to undo it from the panel.
  it("offers a control that re-enables the binding", async () => {
    render(<AgentServiceDetail workspaceId="WS" service={pausedByBinding} />);
    fireEvent.click(screen.getByTestId("agent-service-toggle-schedule"));
    await waitFor(() => {
      expect(mocks.patchAgentService).toHaveBeenCalledWith("scout", {
        binding: { enabled: true },
      });
    });
  });
});
