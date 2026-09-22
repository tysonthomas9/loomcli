/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { act, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { getSkillCapabilities } from "@/api/workspace";
import { skillsStore } from "@/stores/skillsStore";
import { useSkillsActions } from "../useSkills";

vi.mock("@/api/workspace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/workspace")>();
  return { ...actual, getSkillCapabilities: vi.fn() };
});

const mockCapabilities = vi.mocked(getSkillCapabilities);

function CapabilityProbe({ workspaceId }: { workspaceId: string }) {
  const actions = useSkillsActions(workspaceId);
  return (
    <span>
      {actions.canEdit({ kind: "role", role: "reviewer" })
        ? "editable"
        : "read-only"}
    </span>
  );
}

describe("useSkillsActions", () => {
  it("re-renders when role-edit capabilities arrive after the catalog", async () => {
    mockCapabilities.mockResolvedValue({
      can_edit_role_scope: true,
      workspace_scope: "read_only",
    });
    const workspaceId = "skills-actions-capability-rerender";
    render(<CapabilityProbe workspaceId={workspaceId} />);
    expect(screen.getByText("read-only")).toBeInTheDocument();

    await act(() => skillsStore.loadCapabilities(workspaceId));

    expect(screen.getByText("editable")).toBeInTheDocument();
  });
});
