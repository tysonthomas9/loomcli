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
  useCreateLead: () => mockCreateLead,
  useInteractivePrompts: () => ({
    prompts: [
      { id: "lead", label: "Lead" },
      { id: "pr-review", label: "PR Review" },
    ],
    isLoading: false,
    error: null,
  }),
}));

const mockCreateLead = vi.fn();
const leadAgent = { agent_id: "ag_1", name: "lead-nova" };

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
    mockCreateLead.mockReset();
    mockCreateLead.mockResolvedValue(leadAgent);
    mockCreateAgent.mockResolvedValue({
      name: "lead-nova",
      repos: [],
      repo_groups: [],
      cross_repo: false,
    });
  });

  it("creates the Lead card through the Agent API and hands back the agent", async () => {
    const onSuccess = vi.fn();
    const onLeadCreated = vi.fn();

    render(
      <CreateAgentModal
        isOpen
        workspaceId="E2E"
        repos={repos}
        defaultBackend="codex"
        onClose={vi.fn()}
        onSuccess={onSuccess}
        onLeadCreated={onLeadCreated}
      />,
    );

    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "lead-nova" },
    });
    fireEvent.click(screen.getByTestId("create-agent-template-lead"));
    expect(screen.queryByText(/^Lead agent$/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));

    await waitFor(() => expect(onLeadCreated).toHaveBeenCalledWith(leadAgent));
    expect(mockCreateLead.mock.calls[0][0]).toEqual({
      preset: "lead",
      name: "lead-nova",
      repo: "hello-world",
      overrides: { harness: "codex" },
    });
    expect(mockCreateAgent).not.toHaveBeenCalled();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it("asks for a repo before creating a lead", async () => {
    render(
      <CreateAgentModal
        isOpen
        workspaceId="E2E"
        repos={repos}
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "lead-nova" },
    });
    fireEvent.click(screen.getByTestId("create-agent-template-lead"));
    fireEvent.click(screen.getByRole("button", { name: /hello-world/ }));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/pick a repo/i);
    expect(mockCreateLead).not.toHaveBeenCalled();
  });
});
