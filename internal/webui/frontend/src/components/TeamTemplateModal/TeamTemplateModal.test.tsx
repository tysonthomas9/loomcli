/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { describe, expect, it, vi } from "vitest";

import { KeyboardShortcutProvider } from "@/hooks";

import { TeamTemplateModal } from "./TeamTemplateModal";

vi.mock("@/hooks/agents", () => ({
  useTeamTemplates: () => ({
    teamTemplates: [],
    isLoading: false,
    error: null,
    retryCatalog: vi.fn(),
    apply: vi.fn(),
  }),
}));

describe("TeamTemplateModal", () => {
  function renderModal(onClose: () => void) {
    return render(
      <KeyboardShortcutProvider>
        <TeamTemplateModal
          isOpen
          workspaceId="workspace-1"
          workspaceName="Workspace One"
          onClose={onClose}
        />
      </KeyboardShortcutProvider>,
    );
  }

  it("has no in-modal skip affordance and closes from the close button", () => {
    const onClose = vi.fn();
    renderModal(onClose);

    expect(
      screen.queryByRole("button", {
        name: /blank|keep this workspace|skip/i,
      }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Close" }));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes on Escape", () => {
    const onClose = vi.fn();
    renderModal(onClose);

    fireEvent.keyDown(document, { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
