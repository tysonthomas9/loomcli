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

  it("creates a lead agent without repo scope", async () => {
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

    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "lead-nova" },
    });
    fireEvent.change(screen.getByLabelText("Role"), {
      target: { value: "lead" },
    });

    expect(screen.queryByLabelText("Repo")).not.toBeInTheDocument();
    expect(screen.queryByText("Workspace scope")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));

    await waitFor(() => {
      expect(mockCreateAgent).toHaveBeenCalledWith({
        name: "lead-nova",
        role_name: "lead",
        auto: false,
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
