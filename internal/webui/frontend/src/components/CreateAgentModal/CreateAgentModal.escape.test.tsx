/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { KeyboardShortcutProvider } from "@/hooks/ui/useKeyboardShortcuts";

import { CreateAgentModal } from "./CreateAgentModal";

// ESC1: the New Agent dialog closes on Escape, but not while it is creating
// (Cancel is disabled then too).
const mockCreateLead = vi.fn();

vi.mock("@/hooks/agents", () => ({
  useCreateWorkspaceAgent: () => vi.fn(),
  useCreateLead: () => mockCreateLead,
  useLeadHarnesses: () => ["opencode"],
  useInteractivePrompts: () => ({
    prompts: [{ id: "lead", label: "Lead" }],
    isLoading: false,
    error: null,
  }),
}));

const repos = [
  {
    name: "hello-world",
    path: "/tmp/hello-world",
    default_branch: "main",
    remote: "https://github.com/octocat/Hello-World",
    groups: [],
  },
];

function renderModal() {
  const onClose = vi.fn();
  render(
    <KeyboardShortcutProvider>
      <CreateAgentModal
        isOpen
        workspaceId="E2E"
        repos={repos}
        onClose={onClose}
        onSuccess={vi.fn()}
      />
    </KeyboardShortcutProvider>,
  );
  return { onClose };
}

describe("CreateAgentModal: Escape", () => {
  it("closes on Escape from the Name field", () => {
    const { onClose } = renderModal();
    const name = screen.getByTestId("create-agent-name");
    name.focus();

    fireEvent.keyDown(name, { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("ignores Escape while the agent is being created", async () => {
    mockCreateLead.mockReturnValue(new Promise(() => {}));
    const { onClose } = renderModal();
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "lead-nova" },
    });
    fireEvent.click(screen.getByTestId("create-agent-template-lead"));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    await waitFor(() => expect(mockCreateLead).toHaveBeenCalled());
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();

    fireEvent.keyDown(document, { key: "Escape" });

    expect(onClose).not.toHaveBeenCalled();
  });
});
