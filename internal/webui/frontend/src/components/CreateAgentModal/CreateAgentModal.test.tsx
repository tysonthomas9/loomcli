/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { CreateAgentModal } from "./CreateAgentModal";

const mockCreateAgent = vi.fn();

vi.mock("@/hooks/agents", () => ({
  useCreateWorkspaceAgent: () => mockCreateAgent,
}));

describe("CreateAgentModal", () => {
  const repos = [
    {
      name: "hello-world",
      path: "/tmp/hello-world",
      default_branch: "main",
      remote: "https://github.com/octocat/Hello-World",
      groups: [],
    },
  ];

  beforeEach(() => {
    mockCreateAgent.mockReset();
    mockCreateAgent.mockResolvedValue({
      name: "lead-nova",
      repos: [],
      repo_groups: [],
      cross_repo: false,
    });
  });

  it("creates a lead agent with workspace scope when no repo chip is selected", async () => {
    const onSuccess = vi.fn();

    render(
      <CreateAgentModal
        isOpen
        workspaceId="E2E"
        repos={repos}
        defaultBackend="codex"
        onClose={vi.fn()}
        onSuccess={onSuccess}
      />,
    );

    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "lead-nova" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Lead" }));
    // The first repo chip is pre-selected; deselect it so the lead gets
    // workspace-wide scope (empty selection = cross_repo).
    fireEvent.click(screen.getByRole("button", { name: /hello-world/ }));

    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));

    await waitFor(() => {
      expect(mockCreateAgent).toHaveBeenCalledWith({
        name: "lead-nova",
        role_name: "lead",
        auto: false,
        cross_repo: true,
        repos: [],
        backend: "codex",
      });
    });
    expect(onSuccess).toHaveBeenCalledWith({
      name: "lead-nova",
      repos: [],
      repo_groups: [],
      cross_repo: false,
    });
  });
});
