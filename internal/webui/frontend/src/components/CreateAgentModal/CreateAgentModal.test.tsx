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
  useLeadHarnesses: () => ["opencode"],
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

  it("creates the Lead card through the Agent API on a harness the server runs", async () => {
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
    // The workspace default is codex, which this server cannot run a lead on.
    expect(screen.getByTestId("create-agent-backend")).toHaveValue("opencode");
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));

    await waitFor(() => expect(onLeadCreated).toHaveBeenCalledWith(leadAgent));
    expect(mockCreateLead.mock.calls[0][0]).toEqual({
      preset: "lead",
      name: "lead-nova",
      repo: "/tmp/hello-world",
      base_ref: "main",
      overrides: { harness: "opencode" },
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
    expect(
      screen.getByText("Pick the repo this lead works in."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/workspace-wide scope/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/workspace scope/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/pick a repo/i);
    expect(mockCreateLead).not.toHaveBeenCalled();
  });

  it("tells a lead it needs a repo when the workspace has none", () => {
    render(
      <CreateAgentModal
        isOpen
        workspaceId="E2E"
        repos={[]}
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId("create-agent-template-lead"));
    expect(screen.getByTestId("create-agent-no-repos")).toHaveTextContent(
      "No repos yet — add one from the sidebar first. A lead needs a repo to work in.",
    );
    expect(screen.queryByText(/workspace scope/i)).not.toBeInTheDocument();
  });
});
