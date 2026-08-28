/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { SkillsBrowserOverlays } from "../skills";

const mocks = vi.hoisted(() => ({
  createSkill: vi.fn(),
  showToast: vi.fn(),
  denyEdit: () => {},
}));

vi.mock("@/hooks", () => ({
  useWorkspaceContext: () => ({ workspaceId: "ws-1" }),
  useSkillsActions: () => ({
    createSkill: mocks.createSkill,
    deleteSkill: vi.fn(),
  }),
  useFileDocumentRegistry: () => ({ resetPathPrefix: vi.fn() }),
  useFileBrowserStoreInstance: () => ({
    getState: () => ({ closePathPrefix: vi.fn() }),
  }),
  useToast: () => ({ showToast: mocks.showToast }),
}));

const roleRef = {
  kind: "skills" as const,
  group: { kind: "role" as const, role: "reviewer" },
};

function DeniedCreateHarness() {
  const [editable, setEditable] = useState(true);
  mocks.denyEdit = () => setEditable(false);
  return (
    <SkillsBrowserOverlays
      menu={null}
      newSkillRef={roleRef}
      deleteSkill={null}
      canEdit={() => editable}
      onChooseNew={vi.fn()}
      onCloseMenu={vi.fn()}
      onCancelNew={vi.fn()}
      onCancelDelete={vi.fn()}
      onSkillCreated={vi.fn()}
      onSkillDeleted={vi.fn()}
    />
  );
}

describe("SkillsBrowserOverlays", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.denyEdit = () => {};
  });

  it("toasts a create 403 even when the capability change unmounts the dialog", async () => {
    mocks.createSkill.mockImplementation(async () => {
      mocks.denyEdit();
      throw new Error("role skill creation is forbidden");
    });
    render(<DeniedCreateHarness />);
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "audit" },
    });
    fireEvent.change(screen.getByLabelText("Description"), {
      target: { value: "Audit changes" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(mocks.showToast).toHaveBeenCalledWith(
        "role skill creation is forbidden",
        { type: "error" },
      ),
    );
    expect(screen.queryByRole("dialog", { name: "New skill" })).toBeNull();
  });
});
