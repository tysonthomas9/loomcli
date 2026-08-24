/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type {
  UseWorkflowsResult,
  UseWorkflowVersionsResult,
} from "@/hooks";

const useWorkflowsMock = vi.fn<[], UseWorkflowsResult>();
const useWorkflowVersionsMock = vi.fn<[], UseWorkflowVersionsResult>();

vi.mock("react-router-dom", () => ({
  useParams: () => ({ workspaceId: "W" }),
}));

vi.mock("@/hooks", () => ({
  useWorkflows: () => useWorkflowsMock(),
  useWorkflowVersions: () => useWorkflowVersionsMock(),
}));

import { WorkflowsDashboard } from "../WorkflowsDashboard";

function versionsResult(
  overrides: Partial<UseWorkflowVersionsResult> = {},
): UseWorkflowVersionsResult {
  return {
    data: {
      driver_id: "epic-runner",
      versions: [
        {
          version: { version_id: "v1", driver_id: "epic-runner", version: 1 },
          active: true,
          approved: true,
          effective_trust: "trusted",
          bundle_verified: true,
        },
      ],
    },
    isLoading: false,
    error: null,
    actionPending: false,
    actionError: null,
    refetch: vi.fn(),
    approve: vi.fn().mockResolvedValue(undefined),
    unapprove: vi.fn().mockResolvedValue(undefined),
    activate: vi.fn().mockResolvedValue(undefined),
    rollback: vi.fn().mockResolvedValue(undefined),
    adoptBuiltin: vi.fn().mockResolvedValue(undefined),
    authorVersion: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

describe("WorkflowsDashboard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useWorkflowsMock.mockReturnValue({
      workflows: [
        {
          driver_id: "epic-runner",
          name: "epic-runner",
          status: "active",
          active_version_id: "v1",
          built_in: true,
          approved: true,
          effective_trust: "trusted",
        },
        {
          driver_id: "my-flow",
          name: "my-flow",
          status: "draft",
          built_in: false,
          approved: false,
        },
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    useWorkflowVersionsMock.mockReturnValue(versionsResult());
  });

  it("lists workflows and auto-selects the first, rendering its detail", () => {
    render(<WorkflowsDashboard />);
    const list = screen.getByTestId("workflow-list");
    expect(within(list).getByTestId("workflow-item-epic-runner")).toBeInTheDocument();
    expect(within(list).getByTestId("workflow-item-my-flow")).toBeInTheDocument();
    // First workflow auto-selected → its detail + versions table render.
    expect(screen.getByTestId("workflow-detail")).toBeInTheDocument();
    expect(screen.getByTestId("versions-table")).toBeInTheDocument();
  });

  it("shows the built-in update banner when an update is available", () => {
    useWorkflowVersionsMock.mockReturnValue(
      versionsResult({
        data: {
          driver_id: "epic-runner",
          versions: [],
          builtin: {
            packaged_version_id: "epic-runner-v-new",
            packaged_source_digest: "sha256:aaaa",
            packaged_artifact_digest: "sha256:bbbb",
            track: "pinned",
            update_available: true,
            previous_active_version_id: "",
          },
        },
      }),
    );
    render(<WorkflowsDashboard />);
    expect(screen.getByTestId("builtin-update-banner")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("adopt-builtin-update"));
    expect(useWorkflowVersionsMock.mock.results[0]?.value.adoptBuiltin).toHaveBeenCalled();
  });

  it("refreshes the workflow list after a successful detail-panel action", async () => {
    const refetch = vi.fn();
    useWorkflowsMock.mockReturnValue({
      workflows: [
        {
          driver_id: "epic-runner",
          name: "epic-runner",
          status: "active",
          active_version_id: "v1",
          built_in: true,
          approved: true,
          effective_trust: "trusted",
        },
      ],
      isLoading: false,
      error: null,
      refetch,
    });
    const unapprove = vi.fn().mockResolvedValue(undefined);
    useWorkflowVersionsMock.mockReturnValue(versionsResult({ unapprove }));

    render(<WorkflowsDashboard />);
    fireEvent.click(screen.getByTestId("unapprove-v1"));

    expect(unapprove).toHaveBeenCalledWith("v1");
    // onMutated wiring: the left-rail list refetches so its trust badge cannot
    // go stale after a lifecycle action taken in the detail panel.
    await waitFor(() => expect(refetch).toHaveBeenCalled());
  });

  it("surfaces a list error", () => {
    useWorkflowsMock.mockReturnValue({
      workflows: [],
      isLoading: false,
      error: new Error("boom"),
      refetch: vi.fn(),
    });
    render(<WorkflowsDashboard />);
    expect(screen.getByTestId("workflows-error")).toHaveTextContent("boom");
  });
});
