/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import "@testing-library/jest-dom";

import { CreateAgentModal } from "./CreateAgentModal";

const mockCreateAgent = vi.fn();
const mockEnsureRole = vi.fn().mockResolvedValue({ name: "bug-triage" });
const mockListWorkspaceRoles = vi.fn();
const mockCreateBinding = vi.fn();
const mockEnsureConnector = vi.fn();
const mockAddGrant = vi.fn();

vi.mock("@/hooks/agents", () => ({
  useCreateWorkspaceAgent: () => mockCreateAgent,
  useEnsureWorkspaceRole: () => mockEnsureRole,
  useInteractivePrompts: () => ({
    prompts: [
      { id: "lead", label: "Lead" },
      { id: "pr-review", label: "PR Review" },
    ],
    isLoading: false,
    error: null,
  }),
}));
vi.mock("@/api/workspace", () => ({
  listWorkspaceRoles: (...args: unknown[]) => mockListWorkspaceRoles(...args),
}));
vi.mock("@/hooks/workspace", () => ({
  GITHUB_CONNECTOR_ID: "github",
  useAutomations: () => ({ createBinding: mockCreateBinding }),
  useBackends: () => ({
    backends: [{ name: "codex", displayName: "codex" }],
  }),
  useConnectorProvisioning: () => ({
    ensureConnector: mockEnsureConnector,
    addGrant: mockAddGrant,
  }),
  useLocalSettings: () => ({
    settings: { runtime_credentials: { github: { configured: true } } },
  }),
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
    mockListWorkspaceRoles.mockReset();
    mockListWorkspaceRoles.mockResolvedValue([]);
    mockCreateAgent.mockResolvedValue({
      name: "lead-nova",
      repos: [],
      repo_groups: [],
      cross_repo: false,
    });
  });

  it("creates the Lead interactive card with the legacy lead payload", async () => {
    const onSuccess = vi.fn();

    render(
      <MemoryRouter initialEntries={["/ws/E2E/agents"]}>
        <CreateAgentModal
          isOpen
          workspaceId="E2E"
          repos={repos}
          defaultBackend="codex"
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "lead-nova" },
    });
    fireEvent.click(screen.getByTestId("create-agent-template-lead"));
    expect(screen.queryByText(/^Lead agent$/i)).not.toBeInTheDocument();
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
    expect(mockCreateAgent.mock.calls[0][0]).not.toHaveProperty("kind");
    expect(mockCreateAgent.mock.calls[0][0]).not.toHaveProperty("prompt_file");
    expect(onSuccess).toHaveBeenCalledWith({
      name: "lead-nova",
      repos: [],
      repo_groups: [],
      cross_repo: false,
    });
  });
});
