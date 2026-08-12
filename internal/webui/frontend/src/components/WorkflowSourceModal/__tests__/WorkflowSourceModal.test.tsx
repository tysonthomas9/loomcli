/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { WorkflowSourceModal } from "../WorkflowSourceModal";

const workflow = vi.hoisted(() => ({
  getSource: vi.fn(),
  listVersions: vi.fn(),
  saveSource: vi.fn(),
  approveVersion: vi.fn(),
  activateVersion: vi.fn(),
}));
const showToast = vi.hoisted(() => vi.fn());

vi.mock("@/hooks/workflows/useWorkflowSource", () => ({
  useWorkflowSource: () => workflow,
}));

vi.mock("@/hooks/ui", () => ({
  useToast: () => ({ showToast }),
}));

const version = {
  workspace_key: "TEST",
  driver_id: "driver-1",
  version_id: "version-1",
  version: 1,
  source_digest: "source-digest",
  bundle_digest: "bundle-digest",
  validation_status: "passed",
  manifest: {},
};

beforeEach(() => {
  vi.clearAllMocks();
  workflow.getSource.mockResolvedValue({
    files: { "workflow.ts": "export default {};" },
    entrypoint: "workflow.ts",
  });
  workflow.listVersions.mockResolvedValue({
    driver: {
      workspace_key: "TEST",
      driver_id: "driver-1",
      name: "demo",
      status: "draft",
      metadata: {},
      created_at: "2026-07-15T00:00:00Z",
      updated_at: "2026-07-15T00:00:00Z",
    },
    driver_id: "driver-1",
    active_version_id: "",
    versions: [version],
  });
  workflow.saveSource.mockResolvedValue({
    driver: { driver_id: "driver-1" },
    version,
    created_driver: false,
    created_version: true,
    reused_version: false,
    activated: false,
    build_diagnostics: "ok",
  });
});

describe("WorkflowSourceModal Workflow Catalog boundary", () => {
  it("builds an inactive draft instead of requesting legacy activation", async () => {
    render(
      <WorkflowSourceModal
        isOpen
        workspaceId="TEST"
        workflowName="demo"
        onClose={vi.fn()}
      />,
    );

    const build = await screen.findByRole("button", { name: "Build draft" });
    fireEvent.click(build);

    await waitFor(() =>
      expect(workflow.saveSource).toHaveBeenCalledWith("demo", {
        files: { "workflow.ts": "export default {};" },
        entrypoint: "workflow.ts",
        activate: false,
      }),
    );
    expect(showToast).toHaveBeenCalledWith("demo built as an inactive draft", {
      type: "success",
    });
  });

  it("exposes lifecycle controls in a raw loopback browser", async () => {
    render(
      <WorkflowSourceModal
        isOpen
        workspaceId="TEST"
        workflowName="demo"
        onClose={vi.fn()}
      />,
    );

    expect(await screen.findByTestId("workflow-source-versions")).toBeVisible();
    expect(
      screen.queryByTestId("workflow-lifecycle-desktop-required"),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Activate" })).toBeDisabled();
    expect(workflow.approveVersion).not.toHaveBeenCalled();
    expect(workflow.activateVersion).not.toHaveBeenCalled();
  });

  it("does not present a malformed approval marker as approved", async () => {
    workflow.listVersions.mockResolvedValue({
      driver: {
        driver_id: "driver-1",
        metadata: { "approved_version:version-1": "wrong-digest" },
      },
      driver_id: "driver-1",
      active_version_id: "",
      versions: [version],
    });

    render(
      <WorkflowSourceModal
        isOpen
        workspaceId="TEST"
        workflowName="demo"
        onClose={vi.fn()}
      />,
    );

    expect(
      await screen.findByRole("button", { name: "Approve" }),
    ).toBeEnabled();
    expect(screen.getByRole("button", { name: "Activate" })).toBeDisabled();
  });

  it("preserves the approve then activate journey", async () => {
    workflow.approveVersion.mockResolvedValue({
      action: "approve",
      driver: { driver_id: "driver-1" },
      version,
    });
    workflow.activateVersion.mockResolvedValue({
      action: "activate",
      driver: { driver_id: "driver-1" },
      version,
    });
    workflow.listVersions
      .mockResolvedValueOnce({
        driver: { driver_id: "driver-1", metadata: {} },
        driver_id: "driver-1",
        active_version_id: "",
        versions: [version],
      })
      .mockResolvedValueOnce({
        driver: {
          driver_id: "driver-1",
          metadata: { "approved_version:version-1": "source-digest" },
        },
        driver_id: "driver-1",
        active_version_id: "",
        versions: [version],
      })
      .mockResolvedValueOnce({
        driver: {
          driver_id: "driver-1",
          metadata: { "approved_version:version-1": "source-digest" },
        },
        driver_id: "driver-1",
        active_version_id: "version-1",
        versions: [version],
      });

    render(
      <WorkflowSourceModal
        isOpen
        workspaceId="TEST"
        workflowName="demo"
        onClose={vi.fn()}
      />,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(workflow.approveVersion).toHaveBeenCalledWith("demo", "version-1"),
    );
    const activate = await screen.findByRole("button", { name: "Activate" });
    await waitFor(() => expect(activate).toBeEnabled());
    fireEvent.click(activate);
    await waitFor(() =>
      expect(workflow.activateVersion).toHaveBeenCalledWith(
        "demo",
        "version-1",
      ),
    );
    expect(showToast).toHaveBeenCalledWith("Version approved", {
      type: "success",
    });
    expect(showToast).toHaveBeenCalledWith("Version activated", {
      type: "success",
    });
  });
});
