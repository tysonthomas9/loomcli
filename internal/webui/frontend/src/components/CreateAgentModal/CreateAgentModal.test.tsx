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
const mockGetWorkspaceRole = vi.fn();
const mockCreateBinding = vi.fn();
const mockUpdateBinding = vi.fn();
const mockSetEnabled = vi.fn();
const mockPreflightCredential = vi.fn();
const mockEnsureConnector = vi.fn();
const mockAddGrant = vi.fn();
const mockReplaceGrants = vi.fn();

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
  getWorkspaceRole: (...args: unknown[]) => mockGetWorkspaceRole(...args),
  listWorkspaceRoles: (...args: unknown[]) => mockListWorkspaceRoles(...args),
}));
vi.mock("@/hooks/workspace", () => ({
  GITHUB_CONNECTOR_ID: "github",
  useAutomations: () => ({
    createBinding: mockCreateBinding,
    updateBinding: mockUpdateBinding,
    setEnabled: mockSetEnabled,
  }),
  useBackends: () => ({
    backends: [
      {
        name: "codex",
        displayName: "Codex",
        available: true,
        installed: true,
      },
    ],
    isLoading: false,
    error: null,
  }),
  useConnectorProvisioning: () => ({
    preflightCredential: mockPreflightCredential,
    ensureConnector: mockEnsureConnector,
    addGrant: mockAddGrant,
    replaceGrants: mockReplaceGrants,
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
    mockGetWorkspaceRole.mockReset();
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
    expect(screen.getByTestId("create-agent-repo-chips")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /hello-world/i }),
    ).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));

    await waitFor(() => {
      expect(mockCreateAgent).toHaveBeenCalledWith({
        name: "lead-nova",
        role_name: "lead",
        auto: false,
        cross_repo: false,
        repos: ["hello-world"],
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
      kind: "interactive",
    });
  });
});
