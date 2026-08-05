/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CreateAgentModal.
 *
 * Covers the v5-era validation surface plus the newer defaults behavior
 * introduced with onboarding (defaultName / defaultRole props +
 * wasOpenRef gating so the form doesn't reset state on every parent
 * re-render while the modal is open).
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { MemoryRouter, useLocation } from "react-router-dom";
import "@testing-library/jest-dom";

import { CreateAgentModal } from "../CreateAgentModal";
import { BUG_TRIAGE_PROMPT } from "../agentTemplates";
import { ApiError } from "@/types/common";
import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";

// ---------- Mocks ----------

// useCreateWorkspaceAgent returns a function (request) => Promise<agent>.
// Tests swap the function per case via mockCreateAgent.mockImplementation.
const mockCreateAgent = vi.fn();
const mockCreatePromptAgentRecord = vi.fn();
const mockListWorkspaceRoles = vi.fn();
const mockGetWorkspaceRole = vi.fn();
const mockCreateBinding = vi.fn();
const mockUpdateBinding = vi.fn();
const mockSetEnabled = vi.fn();
const mockPreflightCredential = vi.fn();
const mockEnsureConnector = vi.fn();
const mockAddGrant = vi.fn();
const mockReplaceGrants = vi.fn();
const mockUseInteractivePrompts = vi.fn();
const mockUseLocalSettings = vi.fn();
const mockUseBackends = vi.fn();

vi.mock("@/hooks/agents", () => ({
  useCreateWorkspaceAgent: () => mockCreateAgent,
  useInteractivePrompts: (...args: unknown[]) =>
    mockUseInteractivePrompts(...args),
}));
vi.mock("@/api/agents", () => ({
  createPromptAgentRecord: (...args: unknown[]) =>
    mockCreatePromptAgentRecord(...args),
}));
vi.mock("@/api/workspace", () => ({
  getWorkspaceRole: (...args: unknown[]) => mockGetWorkspaceRole(...args),
  listWorkspaceRoles: (...args: unknown[]) => mockListWorkspaceRoles(...args),
}));
vi.mock("@/hooks/workspace", () => ({
  GITHUB_CONNECTOR_ID: "github",
  dispatchBindingsChanged: vi.fn(),
  useAutomations: () => ({
    createBinding: mockCreateBinding,
    updateBinding: mockUpdateBinding,
    setEnabled: mockSetEnabled,
  }),
  useBackends: (...args: unknown[]) => mockUseBackends(...args),
  useConnectorProvisioning: () => ({
    preflightCredential: mockPreflightCredential,
    ensureConnector: mockEnsureConnector,
    addGrant: mockAddGrant,
    replaceGrants: mockReplaceGrants,
  }),
  useLocalSettings: (...args: unknown[]) => mockUseLocalSettings(...args),
}));

// ---------- Helpers ----------

const repos: RepoInfo[] = [
  { name: "alpha", default_branch: "main", local_path: "/a" },
  { name: "beta", default_branch: "main", local_path: "/b" },
];

const workflowRepos: RepoInfo[] = [
  {
    name: "alpha",
    path: "/a",
    default_branch: "main",
    remote: "git@github.com:acme/alpha.git",
    groups: [],
  },
  {
    name: "beta",
    path: "/b",
    default_branch: "main",
    remote: "https://github.com/acme/beta.git",
    groups: [],
  },
];

const sampleAgent: WorkspaceAgentInfo = {
  name: "planner",
  repos: ["alpha"],
  repo_groups: [],
  cross_repo: false,
};

const sampleAgentRecord = {
  id: "agt-coder",
  name: "coder",
  kind: "prompt",
  enabled: true,
  behavior: { role_name: "task" },
  workspace_key: "ws-1",
  bindings: [{ binding_id: "agt-coder-1" }],
};

function renderModal(
  overrides: Partial<React.ComponentProps<typeof CreateAgentModal>> = {},
) {
  const onClose = vi.fn();
  const onSuccess = vi.fn();
  const utils = render(
    <MemoryRouter initialEntries={["/ws/ws-1/agents"]}>
      <CreateAgentModal
        isOpen
        workspaceId="ws-1"
        repos={repos}
        onClose={onClose}
        onSuccess={onSuccess}
        {...overrides}
      />
      <RouteProbe />
    </MemoryRouter>,
  );
  return { ...utils, onClose, onSuccess };
}

function RouteProbe(): JSX.Element {
  const location = useLocation();
  return <div data-testid="route-probe">{location.pathname}</div>;
}

function modalWithRouter(
  props: React.ComponentProps<typeof CreateAgentModal>,
): JSX.Element {
  return (
    <MemoryRouter initialEntries={["/ws/ws-1/agents"]}>
      <CreateAgentModal {...props} />
    </MemoryRouter>
  );
}

beforeEach(() => {
  mockCreateAgent.mockReset();
  mockCreatePromptAgentRecord.mockReset();
  mockCreateBinding.mockReset();
  mockUpdateBinding.mockReset();
  mockSetEnabled.mockReset();
  mockPreflightCredential.mockReset();
  mockEnsureConnector.mockReset();
  mockAddGrant.mockReset();
  mockReplaceGrants.mockReset();
  mockListWorkspaceRoles.mockReset();
  mockGetWorkspaceRole.mockReset();
  mockCreatePromptAgentRecord.mockResolvedValue(sampleAgentRecord);
  mockListWorkspaceRoles.mockResolvedValue([]);
  mockGetWorkspaceRole.mockImplementation(
    (_workspaceId: string, name: string) =>
      Promise.resolve({
        role: {
          workspace_key: "ws-1",
          name,
          kind: "worker",
          task_filter: "has_design",
        },
        prompt: `${name} prompt`,
      }),
  );
  mockCreateBinding.mockResolvedValue({ binding_id: "binding" });
  mockUpdateBinding.mockResolvedValue({
    binding_id: "binding",
    created_at: "2026-07-23T12:00:00Z",
    updated_at: "2026-07-23T12:00:01Z",
  });
  mockSetEnabled.mockResolvedValue(undefined);
  mockPreflightCredential.mockResolvedValue({
    provider: "github",
    configured: true,
    usable: true,
  });
  mockEnsureConnector.mockResolvedValue(undefined);
  mockAddGrant.mockResolvedValue(undefined);
  mockReplaceGrants.mockResolvedValue(undefined);
  mockUseLocalSettings.mockReset();
  mockUseLocalSettings.mockReturnValue({
    settings: { runtime_credentials: { github: { configured: true } } },
  });
  mockUseBackends.mockReset();
  mockUseBackends.mockReturnValue({
    backends: [
      {
        name: "codex",
        displayName: "Codex",
        available: true,
        installed: true,
      },
      {
        name: "claude",
        displayName: "Claude",
        available: true,
        installed: true,
      },
    ],
    isLoading: false,
    error: null,
  });
  mockUseInteractivePrompts.mockReset();
  mockUseInteractivePrompts.mockReturnValue({
    prompts: [
      { id: "lead", label: "Lead" },
      { id: "pr-review", label: "PR Review" },
    ],
    isLoading: false,
    error: null,
  });
});

// ---------- isOpen gate ----------

describe("CreateAgentModal: open/close gate", () => {
  it("renders nothing when isOpen is false", () => {
    const { container } = render(
      modalWithRouter({
        isOpen: false,
        workspaceId: "ws-1",
        repos,
        onClose: vi.fn(),
        onSuccess: vi.fn(),
      }),
    );
    expect(container.firstChild).toBeNull();
  });

  it("keeps sibling route content mounted and disables closed-modal reads", () => {
    render(
      <MemoryRouter initialEntries={["/ws/ws-1/agents"]}>
        <CreateAgentModal
          isOpen={false}
          workspaceId="ws-1"
          repos={repos}
          onClose={vi.fn()}
          onSuccess={vi.fn()}
        />
        <RouteProbe />
      </MemoryRouter>,
    );

    expect(screen.queryByRole("dialog", { name: /new agent/i })).toBeNull();
    expect(screen.getByTestId("route-probe")).toHaveTextContent(
      "/ws/ws-1/agents",
    );
    expect(mockListWorkspaceRoles).not.toHaveBeenCalled();
    expect(mockUseBackends).toHaveBeenCalledWith(false);
    expect(mockUseLocalSettings).toHaveBeenCalledWith(false);
    expect(mockUseInteractivePrompts).toHaveBeenCalledWith("ws-1", false);
  });

  it("renders the dialog when isOpen is true", () => {
    renderModal();
    expect(
      screen.getByRole("dialog", { name: /new agent/i }),
    ).toBeInTheDocument();
  });
});

// ---------- defaults props ----------

describe("CreateAgentModal: default prop seeding", () => {
  it("seeds name input from defaultName", () => {
    renderModal({ defaultName: "starter-agent" });
    expect(screen.getByTestId("create-agent-name")).toHaveValue(
      "starter-agent",
    );
  });

  it("trims whitespace in defaultName", () => {
    renderModal({ defaultName: "  pad  " });
    expect(screen.getByTestId("create-agent-name")).toHaveValue("pad");
  });

  it("seeds the Planner template from defaultRole plan", () => {
    renderModal({ defaultRole: "plan" });
    expect(screen.getByTestId("create-agent-template-planner")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("defaults to Coder template when defaultRole is omitted", () => {
    renderModal();
    expect(screen.getByTestId("create-agent-template-task")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("seeds backend from defaultBackend (falling back to 'codex')", () => {
    renderModal({ defaultBackend: "claude" });
    expect(screen.getByTestId("create-agent-backend")).toHaveValue("claude");
  });

  it("falls back to 'codex' backend when defaultBackend is empty", () => {
    renderModal({ defaultBackend: "   " });
    expect(screen.getByTestId("create-agent-backend")).toHaveValue("codex");
  });

  it("filters unavailable and uninstalled backends and selects a healthy fallback", async () => {
    mockUseBackends.mockReturnValue({
      backends: [
        {
          name: "codex",
          displayName: "Codex",
          available: false,
          installed: true,
        },
        {
          name: "opencode",
          displayName: "OpenCode",
          available: true,
          installed: false,
        },
        {
          name: "claude",
          displayName: "Claude",
          available: true,
          installed: true,
        },
      ],
      isLoading: false,
      error: null,
    });
    renderModal({ defaultBackend: "codex" });

    await waitFor(() =>
      expect(screen.getByTestId("create-agent-backend")).toHaveValue("claude"),
    );
    expect(
      screen.queryByRole("option", { name: "Codex" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: "OpenCode" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Claude" })).toBeInTheDocument();
  });

  it("renders built-in interactive prompts as cards without a standalone Lead section", () => {
    renderModal();
    expect(
      screen.getByTestId("create-agent-template-lead"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("create-agent-template-interactive-pr-review"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("create-agent-template-custom-prompt"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/^Lead agent$/i)).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("create-agent-interactive-builtin"),
    ).not.toBeInTheDocument();
  });

  it("constrains creation to the requested canonical role behavior", () => {
    renderModal({ requiredRole: "task", defaultName: "review-worker" });

    expect(
      screen.getByTestId("create-agent-required-role-mode"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("create-agent-template-task")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(
      screen.queryByTestId("create-agent-template-interactive-pr-review"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("create-agent-template-planner"),
    ).not.toBeInTheDocument();
  });

  it("exposes only persisted roles that prompt-agent can execute", async () => {
    mockListWorkspaceRoles.mockResolvedValueOnce([
      { workspace_key: "ws-1", name: "pr-review", kind: "interactive" },
      { workspace_key: "ws-1", name: "orchestrator" },
      {
        workspace_key: "ws-1",
        name: "docs-worker",
        kind: "worker",
        task_filter: "has_design",
      },
      {
        workspace_key: "ws-1",
        name: "documentation-review",
        kind: "worker",
        task_filter: "review",
      },
      {
        workspace_key: "ws-1",
        name: "read-only-documentation-review",
        kind: "worker",
        task_filter: "review",
        read_only: true,
      },
      {
        workspace_key: "ws-1",
        name: "bug-triage",
        kind: "worker",
        task_filter: "bug",
        read_only: true,
      },
      {
        workspace_key: "ws-1",
        name: "unsafe-bug-triage",
        kind: "worker",
        task_filter: "bug",
        read_only: false,
      },
      {
        workspace_key: "ws-1",
        name: "missing-prompt",
        kind: "worker",
        task_filter: "any",
      },
      {
        workspace_key: "ws-1",
        name: "legacy-filter",
        kind: "worker",
        task_filter: "needs_design",
      },
    ]);
    mockGetWorkspaceRole.mockImplementation(
      (_workspaceId: string, name: string) =>
        Promise.resolve({
          role: {
            workspace_key: "ws-1",
            name,
            kind: "worker",
            task_filter:
              name === "docs-worker"
                ? "has_design"
                : name === "documentation-review"
                  ? "review"
                  : name === "bug-triage"
                    ? "bug"
                    : "any",
            read_only: name === "bug-triage",
          },
          prompt:
            name === "docs-worker"
              ? "Write documentation."
              : name === "documentation-review"
                ? "Review and update documentation."
                : name === "bug-triage"
                  ? "Triage the assigned bug."
                  : "",
        }),
    );

    renderModal();

    expect(
      await screen.findByTestId("create-agent-template-role-docs-worker"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("create-agent-template-bug-triage"),
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByTestId("create-agent-template-role-documentation-review"),
    );
    expect(
      screen.queryByTestId("create-agent-role-trigger-select"),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("Runs when a task enters Review"),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId("create-agent-template-role-pr-review"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("create-agent-template-role-orchestrator"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("create-agent-template-role-missing-prompt"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("create-agent-template-role-legacy-filter"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("create-agent-template-role-unsafe-bug-triage"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId(
        "create-agent-template-role-read-only-documentation-review",
      ),
    ).not.toBeInTheDocument();
    expect(
      screen.getByTestId("create-agent-template-interactive-pr-review"),
    ).toBeInTheDocument();
    expect(mockGetWorkspaceRole).toHaveBeenCalledTimes(3);
  });

  it("does not duplicate the canonical bug-triage behavior from persisted roles", async () => {
    mockListWorkspaceRoles.mockResolvedValueOnce([
      {
        workspace_key: "ws-1",
        name: "bug-triage",
        kind: "worker",
        task_filter: "bug",
        read_only: true,
      },
    ]);

    renderModal();

    expect(
      screen.getByTestId("create-agent-template-bug-triage"),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId("create-agent-template-role-bug-triage"),
    ).not.toBeInTheDocument();
    await waitFor(() => expect(mockListWorkspaceRoles).toHaveBeenCalled());
    expect(mockGetWorkspaceRole).not.toHaveBeenCalled();
  });

  it("submits a hydrated compatible custom role through prompt-agent", async () => {
    mockListWorkspaceRoles.mockResolvedValueOnce([
      {
        workspace_key: "ws-1",
        name: "docs-worker",
        kind: "worker",
        task_filter: "has_design",
      },
    ]);

    renderModal({ defaultName: "docs-agent" });
    fireEvent.click(
      await screen.findByTestId("create-agent-template-role-docs-worker"),
    );
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() =>
      expect(mockCreatePromptAgentRecord).toHaveBeenCalledWith("ws-1", {
        kind: "prompt",
        name: "docs-agent",
        backend: "codex",
        behavior: { role_name: "docs-worker" },
        trigger: {
          source_kind: "internal",
          event_type_patterns: ["internal.task.ready"],
        },
        enabled: true,
      }),
    );
  });

  it("submits a hydrated review role through its review trigger", async () => {
    mockListWorkspaceRoles.mockResolvedValueOnce([
      {
        workspace_key: "ws-1",
        name: "documentation",
        kind: "worker",
        task_filter: "review",
      },
    ]);
    mockGetWorkspaceRole.mockResolvedValueOnce({
      role: {
        workspace_key: "ws-1",
        name: "documentation",
        kind: "worker",
        task_filter: "review",
      },
      prompt: "Review and update documentation.",
    });

    renderModal({ defaultName: "docs-agent" });
    fireEvent.click(
      await screen.findByTestId("create-agent-template-role-documentation"),
    );
    expect(
      screen.queryByTestId("create-agent-role-trigger-select"),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("Runs when a task enters Review"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() =>
      expect(mockCreatePromptAgentRecord).toHaveBeenCalledWith("ws-1", {
        kind: "prompt",
        name: "docs-agent",
        backend: "codex",
        behavior: { role_name: "documentation" },
        trigger: {
          source_kind: "internal",
          event_type_patterns: ["internal.task.review"],
        },
        enabled: true,
      }),
    );
  });

  it("keeps repository controls visible for interactive agents", () => {
    renderModal();
    expect(
      screen.queryByTestId("create-agent-repo-chips"),
    ).not.toBeInTheDocument();

    fireEvent.click(
      screen.getByTestId("create-agent-template-interactive-pr-review"),
    );
    expect(screen.getByTestId("create-agent-repo-chips")).toBeInTheDocument();
    expect(
      screen.getByText("Repos", { selector: "#agent-repos-label" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /alpha/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });
});

// ---------- wasOpenRef: don't reset on re-render ----------

describe("CreateAgentModal: state preservation across re-renders", () => {
  it("does not clobber typed name when parent re-renders with same isOpen", () => {
    const { rerender } = render(
      modalWithRouter({
        isOpen: true,
        workspaceId: "ws-1",
        repos,
        defaultName: "seeded",
        onClose: vi.fn(),
        onSuccess: vi.fn(),
      }),
    );
    const nameInput = screen.getByTestId("create-agent-name");
    expect(nameInput).toHaveValue("seeded");

    // User edits the name.
    fireEvent.change(nameInput, { target: { value: "my-custom-name" } });
    expect(nameInput).toHaveValue("my-custom-name");

    // Parent re-renders (e.g., a sibling state changed) with same props.
    rerender(
      modalWithRouter({
        isOpen: true,
        workspaceId: "ws-1",
        repos,
        defaultName: "seeded",
        onClose: vi.fn(),
        onSuccess: vi.fn(),
      }),
    );

    // The wasOpenRef gate must prevent the effect from re-seeding.
    expect(screen.getByTestId("create-agent-name")).toHaveValue(
      "my-custom-name",
    );
  });

  it("re-seeds defaults on close → open transition", () => {
    const { rerender } = render(
      modalWithRouter({
        isOpen: true,
        workspaceId: "ws-1",
        repos,
        defaultName: "first-default",
        onClose: vi.fn(),
        onSuccess: vi.fn(),
      }),
    );
    // User edits.
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "user-typed" },
    });
    fireEvent.click(screen.getByTestId("create-agent-template-new-role"));
    fireEvent.change(screen.getByTestId("create-agent-role-trigger-select"), {
      target: { value: "review" },
    });
    expect(screen.getByTestId("create-agent-role-trigger-select")).toHaveValue(
      "review",
    );
    // Close.
    rerender(
      modalWithRouter({
        isOpen: false,
        workspaceId: "ws-1",
        repos,
        defaultName: "first-default",
        onClose: vi.fn(),
        onSuccess: vi.fn(),
      }),
    );
    // Re-open with a NEW default — should seed the new value.
    rerender(
      modalWithRouter({
        isOpen: true,
        workspaceId: "ws-1",
        repos,
        defaultName: "second-default",
        onClose: vi.fn(),
        onSuccess: vi.fn(),
      }),
    );
    expect(screen.getByTestId("create-agent-name")).toHaveValue(
      "second-default",
    );
    fireEvent.click(screen.getByTestId("create-agent-template-new-role"));
    expect(screen.getByTestId("create-agent-role-trigger-select")).toHaveValue(
      "ready",
    );
  });
});

// ---------- validation ----------

describe("CreateAgentModal: client-side validation", () => {
  it("disables Activate until a name is entered (does not call API)", () => {
    const { onSuccess } = renderModal();
    expect(screen.getByRole("button", { name: /activate/i })).toBeDisabled();
    expect(mockCreateAgent).not.toHaveBeenCalled();
    expect(mockCreatePromptAgentRecord).not.toHaveBeenCalled();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it("keeps Activate disabled for a whitespace-only name", () => {
    renderModal();
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "   " },
    });
    expect(screen.getByRole("button", { name: /activate/i })).toBeDisabled();
    expect(mockCreateAgent).not.toHaveBeenCalled();
    expect(mockCreatePromptAgentRecord).not.toHaveBeenCalled();
  });

  it("shows invalid name feedback inline", () => {
    renderModal();
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "two words" },
    });
    expect(screen.getByTestId("create-agent-name-error")).toHaveTextContent(
      /lowercase letters, numbers, hyphens, dots, or underscores/i,
    );
  });

  it("allows canonical background behavior creation without a workspace repo", () => {
    renderModal({ defaultName: "agent-x", repos: [] });
    fireEvent.click(screen.getByTestId("create-agent-template-task"));
    expect(screen.getByRole("button", { name: /activate/i })).toBeEnabled();
    expect(
      screen.queryByTestId("create-agent-no-repos"),
    ).not.toBeInTheDocument();
    expect(mockCreateAgent).not.toHaveBeenCalled();
  });

  it("blocks even programmatic submission while backend health is unresolved", async () => {
    mockUseBackends.mockReturnValue({
      backends: [
        {
          name: "codex",
          displayName: "Codex",
          available: true,
          installed: true,
        },
      ],
      isLoading: true,
      error: null,
    });
    renderModal({ defaultName: "waiting-agent" });

    expect(screen.getByTestId("create-agent-submit")).toBeDisabled();
    expect(
      screen.getByTestId("create-agent-backend-readiness"),
    ).toHaveTextContent(/checking ai backend availability/i);
    fireEvent.submit(document.getElementById("create-agent-form")!);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /checking ai backend availability/i,
    );
    expect(mockCreatePromptAgentRecord).not.toHaveBeenCalled();
    expect(mockCreateAgent).not.toHaveBeenCalled();
    expect(mockCreateBinding).not.toHaveBeenCalled();
  });
});

// ---------- happy-path submission ----------

describe("CreateAgentModal: submission", () => {
  it("returns a required role-backed agent through onSuccess", async () => {
    mockCreatePromptAgentRecord.mockResolvedValueOnce({
      ...sampleAgentRecord,
      id: "agt-review-worker",
      name: "review-worker",
    });
    const { onSuccess } = renderModal({
      requiredRole: "task",
      defaultName: "review-worker",
    });

    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() =>
      expect(mockCreatePromptAgentRecord).toHaveBeenCalledWith("ws-1", {
        kind: "prompt",
        name: "review-worker",
        backend: "codex",
        behavior: { role_name: "task" },
        trigger: {
          source_kind: "internal",
          event_type_patterns: ["internal.task.ready"],
        },
        enabled: true,
      }),
    );
    expect(mockCreateAgent).not.toHaveBeenCalled();
    expect(onSuccess).toHaveBeenCalledWith({
      name: "review-worker",
      repos: [],
      repo_groups: [],
      cross_repo: true,
      kind: "worker",
      role_name: "task",
      backend: "codex",
    });
  });

  it("creates Bug Triage transactionally with its read-only bug policy", async () => {
    renderModal({ defaultName: "triage-worker" });
    fireEvent.click(screen.getByTestId("create-agent-template-bug-triage"));
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() =>
      expect(mockCreatePromptAgentRecord).toHaveBeenCalledWith("ws-1", {
        kind: "prompt",
        name: "triage-worker",
        backend: "codex",
        behavior: {
          role_name: "bug-triage",
          role_create: {
            prompt: BUG_TRIAGE_PROMPT,
            prompt_filename: "bug-triage.md",
            description:
              "Reproduces and triages ready tickets; does not write fixes.",
            task_filter: "bug",
            read_only: true,
            backend: "codex",
          },
        },
        trigger: {
          source_kind: "internal",
          event_type_patterns: ["internal.task.ready"],
        },
        enabled: true,
      }),
    );
    expect(mockCreateAgent).not.toHaveBeenCalled();
  });

  it("submits mixed-case names as lowercase", async () => {
    renderModal();
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "Test-lead" },
    });
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));
    await waitFor(() => expect(mockCreatePromptAgentRecord).toHaveBeenCalled());
    expect(mockCreatePromptAgentRecord.mock.calls[0][1]).toMatchObject({
      name: "test-lead",
      kind: "prompt",
      behavior: { role_name: "task" },
    });
  });

  it("submits Coder role card through transactional prompt create", async () => {
    const { onClose, onSuccess } = renderModal({
      defaultName: "  planner  ",
      defaultBackend: " claude ",
    });
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() =>
      expect(mockCreatePromptAgentRecord).toHaveBeenCalledTimes(1),
    );
    expect(mockCreatePromptAgentRecord).toHaveBeenCalledWith("ws-1", {
      kind: "prompt",
      name: "planner",
      backend: "claude",
      behavior: { role_name: "task" },
      trigger: {
        source_kind: "internal",
        event_type_patterns: ["internal.task.ready"],
      },
      enabled: true,
    });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onSuccess).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.getByTestId("route-probe")).toHaveTextContent(
        "/ws/ws-1/agents/agt-coder",
      ),
    );
  });

  it("submits the Planner behavior card with its ready-task trigger", async () => {
    renderModal({
      defaultName: "planning-agent",
      defaultBackend: "claude",
    });
    fireEvent.click(screen.getByTestId("create-agent-template-planner"));
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() =>
      expect(mockCreatePromptAgentRecord).toHaveBeenCalledTimes(1),
    );
    expect(mockCreatePromptAgentRecord).toHaveBeenCalledWith("ws-1", {
      kind: "prompt",
      name: "planning-agent",
      backend: "claude",
      behavior: { role_name: "plan" },
      trigger: {
        source_kind: "internal",
        event_type_patterns: ["internal.task.ready"],
      },
      enabled: true,
    });
    expect(mockCreateAgent).not.toHaveBeenCalled();
  });

  it("submits + New role with the role definition in one transaction", async () => {
    renderModal({
      defaultName: "docs-agent",
      defaultBackend: "codex",
    });
    fireEvent.click(screen.getByTestId("create-agent-template-new-role"));
    expect(screen.getByTestId("create-agent-role-trigger-select")).toHaveValue(
      "ready",
    );
    fireEvent.change(screen.getByTestId("create-agent-role-name"), {
      target: { value: "Docs-Assistant" },
    });
    fireEvent.change(screen.getByTestId("create-agent-role-prompt"), {
      target: { value: "Summarize the repository documentation." },
    });
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() =>
      expect(mockCreatePromptAgentRecord).toHaveBeenCalledTimes(1),
    );
    expect(mockCreatePromptAgentRecord).toHaveBeenCalledWith("ws-1", {
      kind: "prompt",
      name: "docs-agent",
      backend: "codex",
      behavior: {
        role_name: "docs-assistant",
        role_create: {
          prompt: "Summarize the repository documentation.",
          prompt_filename: "docs-assistant.md",
          task_filter: "has_design",
          backend: "codex",
        },
      },
      trigger: {
        source_kind: "internal",
        event_type_patterns: ["internal.task.ready"],
      },
      enabled: true,
    });
    expect(mockCreateAgent).not.toHaveBeenCalled();
  });

  it("submits + New role with the Review trigger", async () => {
    renderModal({
      defaultName: "documentation-agent",
      defaultBackend: "codex",
    });
    expect(
      screen.queryByTestId("create-agent-role-trigger-select"),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("create-agent-template-new-role"));
    fireEvent.change(screen.getByTestId("create-agent-role-trigger-select"), {
      target: { value: "review" },
    });
    fireEvent.change(screen.getByTestId("create-agent-role-name"), {
      target: { value: "Documentation" },
    });
    fireEvent.change(screen.getByTestId("create-agent-role-prompt"), {
      target: { value: "Review and update the repository documentation." },
    });
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() =>
      expect(mockCreatePromptAgentRecord).toHaveBeenCalledTimes(1),
    );
    expect(mockCreatePromptAgentRecord).toHaveBeenCalledWith("ws-1", {
      kind: "prompt",
      name: "documentation-agent",
      backend: "codex",
      behavior: {
        role_name: "documentation",
        role_create: {
          prompt: "Review and update the repository documentation.",
          prompt_filename: "documentation.md",
          task_filter: "review",
          backend: "codex",
        },
      },
      trigger: {
        source_kind: "internal",
        event_type_patterns: ["internal.task.review"],
      },
      enabled: true,
    });
    expect(mockCreateAgent).not.toHaveBeenCalled();
  });

  // (The "omit backend when empty" case is unreachable now that AI Backend is a
  // required dropdown — it always carries a value — so that test was removed.)

  it("submits Lead with every explicitly selected repo", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "lead-nova" });
    fireEvent.click(screen.getByTestId("create-agent-template-lead"));
    fireEvent.click(screen.getByRole("button", { name: /beta/i }));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      name: "lead-nova",
      role_name: "lead",
      cross_repo: false,
      repos: ["alpha", "beta"],
    });
    expect(mockCreateAgent.mock.calls[0][0]).not.toHaveProperty("kind");
    expect(mockCreateAgent.mock.calls[0][0]).not.toHaveProperty("prompt_file");
  });

  it("submits PR Review with only the repos the user selected", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "review-nova" });

    fireEvent.click(
      screen.getByTestId("create-agent-template-interactive-pr-review"),
    );
    fireEvent.click(screen.getByRole("button", { name: /alpha/i }));
    fireEvent.click(screen.getByRole("button", { name: /beta/i }));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));

    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      name: "review-nova",
      role_name: "pr-review",
      kind: "interactive",
      prompt_file: "builtin:pr-review",
      cross_repo: false,
      repos: ["beta"],
    });
  });

  it("submits Custom with workspace scope when no repo is selected", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "custom-review" });

    fireEvent.click(screen.getByTestId("create-agent-template-custom-prompt"));
    fireEvent.click(screen.getByRole("button", { name: /alpha/i }));
    const textarea = screen.getByTestId("create-agent-interactive-prompt");
    expect(textarea).toBeInTheDocument();
    fireEvent.change(textarea, {
      target: { value: "  Review literally: {{ marker }}  " },
    });
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));

    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      name: "custom-review",
      role_name: "custom-review",
      kind: "interactive",
      prompt: "Review literally: {{ marker }}",
      cross_repo: true,
      repos: [],
    });
    expect(mockCreateAgent.mock.calls[0][0]).not.toHaveProperty("prompt_file");
  });

  it("fails closed when canonical Bug Triage provisioning conflicts", async () => {
    mockCreatePromptAgentRecord.mockRejectedValueOnce(
      new ApiError("role bug-triage has incompatible configuration", 409),
    );
    renderModal({ defaultName: "triage-conflict" });
    fireEvent.click(screen.getByTestId("create-agent-template-bug-triage"));
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /bug-triage has incompatible configuration/i,
    );
    expect(mockCreatePromptAgentRecord).toHaveBeenCalledTimes(1);
    expect(mockCreateAgent).not.toHaveBeenCalled();
  });

  it("switches background template selection when Planner is clicked", () => {
    renderModal();
    expect(screen.getByTestId("create-agent-template-task")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    fireEvent.click(screen.getByTestId("create-agent-template-planner"));
    expect(screen.getByTestId("create-agent-template-planner")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByTestId("create-agent-template-task")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    expect(screen.getByTestId("create-agent-template-lead")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  it("resets the form to the configured defaults after a successful submit", async () => {
    renderModal({ defaultName: "seed-name", defaultRole: "plan" });

    // User overrides the name.
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "one-off" },
    });
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() => expect(mockCreatePromptAgentRecord).toHaveBeenCalled());
    // After success the form returns to the defaults the parent supplied,
    // not the prior v5-era hard-coded blank/"task".
    await waitFor(() => {
      expect(screen.getByTestId("create-agent-name")).toHaveValue("seed-name");
    });
    expect(screen.getByTestId("create-agent-template-planner")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });
});

// ---------- scripted workflow activation ----------

describe("CreateAgentModal: scripted workflow activation", () => {
  it("collapses multi-repo scope and keeps workflow selection exclusive", () => {
    renderModal({ repos: workflowRepos });
    fireEvent.click(
      screen.getByTestId("create-agent-template-interactive-pr-review"),
    );
    fireEvent.click(screen.getByRole("button", { name: /beta/i }));
    expect(screen.getByRole("button", { name: /alpha/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: /beta/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    fireEvent.click(screen.getByTestId("create-agent-template-local-review"));
    expect(screen.getByRole("button", { name: /alpha/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: /beta/i })).toHaveAttribute(
      "aria-pressed",
      "false",
    );

    fireEvent.click(screen.getByRole("button", { name: /beta/i }));
    expect(screen.getByRole("button", { name: /alpha/i })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    expect(screen.getByRole("button", { name: /beta/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("rejects a workflow without exactly one target before any mutation", async () => {
    renderModal({
      defaultName: "local-review",
      repos: workflowRepos,
    });
    fireEvent.click(screen.getByTestId("create-agent-template-local-review"));
    fireEvent.click(screen.getByRole("button", { name: /alpha/i }));

    expect(screen.getByTestId("create-agent-submit")).toBeDisabled();
    fireEvent.submit(document.getElementById("create-agent-form")!);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Select exactly one target repo for this workflow.",
    );
    expect(mockCreateBinding).not.toHaveBeenCalled();
    expect(mockUpdateBinding).not.toHaveBeenCalled();
    expect(mockSetEnabled).not.toHaveBeenCalled();
    expect(mockEnsureConnector).not.toHaveBeenCalled();
    expect(mockReplaceGrants).not.toHaveBeenCalled();
  });

  it("checks bug-fix workspace backend health without pinning a backend in run input", async () => {
    renderModal({
      defaultName: "bug-fix",
      defaultBackend: "claude",
      repos: workflowRepos,
    });
    fireEvent.click(screen.getByTestId("create-agent-template-bug-fix"));
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() => expect(mockSetEnabled).toHaveBeenCalledTimes(2));
    expect(mockCreateBinding).toHaveBeenCalledWith(
      expect.objectContaining({
        workflow: "bug-fix-agent",
        binding_id: "s1-bug-fix",
        run_input: {
          targetRepo: "alpha",
          githubRepo: "acme/alpha",
        },
        enabled: false,
      }),
    );
    const createInput = mockCreateBinding.mock.calls[0]?.[0]?.run_input;
    expect(createInput).not.toHaveProperty("backend");
    expect(mockPreflightCredential).toHaveBeenCalledWith("github");
    expect(mockPreflightCredential.mock.invocationCallOrder[0]).toBeLessThan(
      mockCreateBinding.mock.invocationCallOrder[0]!,
    );
    expect(mockEnsureConnector).not.toHaveBeenCalled();
    expect(mockAddGrant).not.toHaveBeenCalled();
    expect(mockReplaceGrants).not.toHaveBeenCalled();
  });

  it("reconciles a disabled review binding and scoped grants before enabling it", async () => {
    const onWorkflowActivated = vi.fn();
    renderModal({
      defaultName: "review-beta",
      repos: workflowRepos,
      onWorkflowActivated,
    });

    fireEvent.click(screen.getByTestId("create-agent-template-review-loop"));
    // Workflows have exactly one target. Move the default selection from alpha
    // to beta and prove only beta reaches run_input and connector grants.
    fireEvent.click(screen.getByRole("button", { name: /alpha/i }));
    fireEvent.click(screen.getByRole("button", { name: /beta/i }));
    expect(screen.getByRole("button", { name: /alpha/i })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    expect(screen.getByRole("button", { name: /beta/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() => expect(mockSetEnabled).toHaveBeenCalledTimes(2));
    const runInput = {
      targetRepo: "beta",
      githubRepo: "acme/beta",
    };
    expect(mockCreateBinding).toHaveBeenCalledWith({
      workflow: "review-loop-agent",
      source_kind: "cron",
      schedule: "*/10 * * * *",
      binding_id: "s2-review-loop",
      name: "review-beta",
      run_input: runInput,
      enabled: false,
    });
    expect(mockSetEnabled).toHaveBeenNthCalledWith(1, "s2-review-loop", false);
    expect(mockUpdateBinding).toHaveBeenCalledWith("s2-review-loop", {
      name: "review-beta",
      schedule: "*/10 * * * *",
      run_input: runInput,
    });
    expect(mockEnsureConnector).toHaveBeenCalledWith({
      source: "github",
      connector_id: "github",
      reuse_runtime_credential: true,
    });
    expect(mockReplaceGrants).toHaveBeenCalledWith("github", "s2-review-loop", {
      expected_binding_created_at: "2026-07-23T12:00:00Z",
      expected_binding_updated_at: "2026-07-23T12:00:01Z",
      grants: [
        {
          action: "github.pull_request.read",
          resource_pattern: "repo:acme/beta",
        },
        {
          action: "github.compare.read",
          resource_pattern: "repo:acme/beta",
        },
        {
          action: "github.review.post",
          resource_pattern: "repo:acme/beta",
        },
      ],
    });
    expect(mockAddGrant).not.toHaveBeenCalled();
    expect(mockSetEnabled).toHaveBeenNthCalledWith(2, "s2-review-loop", true);

    const preflightOrder = mockPreflightCredential.mock.invocationCallOrder[0]!;
    const createOrder = mockCreateBinding.mock.invocationCallOrder[0]!;
    const disableOrder = mockSetEnabled.mock.invocationCallOrder[0]!;
    const reconcileOrder = mockUpdateBinding.mock.invocationCallOrder[0]!;
    const connectorOrder = mockEnsureConnector.mock.invocationCallOrder[0]!;
    const grantSetOrder = mockReplaceGrants.mock.invocationCallOrder[0]!;
    const enableOrder = mockSetEnabled.mock.invocationCallOrder[1]!;
    expect(preflightOrder).toBeLessThan(connectorOrder);
    expect(connectorOrder).toBeLessThan(createOrder);
    expect(createOrder).toBeLessThan(disableOrder);
    expect(disableOrder).toBeLessThan(reconcileOrder);
    expect(reconcileOrder).toBeLessThan(grantSetOrder);
    expect(grantSetOrder).toBeLessThan(enableOrder);
    expect(onWorkflowActivated).toHaveBeenCalledWith({
      name: "review-beta",
      workflow: "review-loop-agent",
      bindingId: "s2-review-loop",
    });
  });

  it("rejects an unusable review connector before mutating the singleton binding", async () => {
    mockEnsureConnector.mockRejectedValueOnce(
      new Error("existing connector credential cannot be opened"),
    );
    renderModal({
      defaultName: "review-alpha",
      repos: workflowRepos,
    });
    fireEvent.click(screen.getByTestId("create-agent-template-review-loop"));
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /credential cannot be opened/i,
    );
    expect(mockCreateBinding).not.toHaveBeenCalled();
    expect(mockSetEnabled).not.toHaveBeenCalled();
    expect(mockUpdateBinding).not.toHaveBeenCalled();
    expect(mockReplaceGrants).not.toHaveBeenCalled();
  });

  it.each([
    ["Bug-fix", "create-agent-template-bug-fix"],
    ["Review loop", "create-agent-template-review-loop"],
  ])(
    "blocks %s when the configured GitHub credential cannot be opened, before any authority mutation",
    async (_label, templateTestID) => {
      mockPreflightCredential.mockResolvedValueOnce({
        provider: "github",
        configured: true,
        usable: false,
      });
      renderModal({
        defaultName: "github-workflow",
        repos: workflowRepos,
      });
      fireEvent.click(screen.getByTestId(templateTestID));
      fireEvent.click(screen.getByRole("button", { name: /activate/i }));

      expect(await screen.findByRole("alert")).toHaveTextContent(
        /github token in settings cannot be opened/i,
      );
      expect(mockPreflightCredential).toHaveBeenCalledWith("github");
      expect(mockEnsureConnector).not.toHaveBeenCalled();
      expect(mockCreateBinding).not.toHaveBeenCalled();
      expect(mockSetEnabled).not.toHaveBeenCalled();
      expect(mockUpdateBinding).not.toHaveBeenCalled();
      expect(mockAddGrant).not.toHaveBeenCalled();
      expect(mockReplaceGrants).not.toHaveBeenCalled();
    },
  );

  it("compensates a grant reconciliation failure by leaving the singleton binding disabled", async () => {
    mockReplaceGrants.mockRejectedValueOnce(
      new Error("grant replacement unavailable"),
    );
    renderModal({
      defaultName: "review-alpha",
      repos: workflowRepos,
    });
    fireEvent.click(screen.getByTestId("create-agent-template-review-loop"));
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /grant replacement unavailable/i,
    );
    expect(mockSetEnabled.mock.calls).toEqual([
      ["s2-review-loop", false],
      ["s2-review-loop", false],
    ]);
    expect(
      mockSetEnabled.mock.calls.some(([, enabled]) => enabled === true),
    ).toBe(false);
  });

  it("rolls back to disabled when the final enable attempt fails", async () => {
    mockSetEnabled
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error("enable request failed"))
      .mockResolvedValueOnce(undefined);
    renderModal({
      defaultName: "review-alpha",
      repos: workflowRepos,
    });
    fireEvent.click(screen.getByTestId("create-agent-template-review-loop"));
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /enable request failed/i,
    );
    expect(mockSetEnabled.mock.calls).toEqual([
      ["s2-review-loop", false],
      ["s2-review-loop", true],
      ["s2-review-loop", false],
    ]);
  });

  it("blocks a GitHub workflow before creating a binding when credentials are missing", async () => {
    mockUseLocalSettings.mockReturnValue({
      settings: { runtime_credentials: { github: { configured: false } } },
    });
    renderModal({
      defaultName: "bug-fix",
      repos: workflowRepos,
    });
    fireEvent.click(screen.getByTestId("create-agent-template-bug-fix"));
    expect(
      screen.getByTestId("create-agent-review-needs-github"),
    ).toHaveTextContent(/this workflow requires your settings github token/i);
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /connect a github token in settings/i,
    );
    expect(mockCreateBinding).not.toHaveBeenCalled();
    expect(mockUpdateBinding).not.toHaveBeenCalled();
    expect(mockSetEnabled).not.toHaveBeenCalled();
  });

  it.each([
    ["Review loop", "create-agent-template-review-loop"],
    ["Local review", "create-agent-template-local-review"],
  ])(
    "blocks %s before any mutation when Codex is unavailable",
    async (_label, templateTestID) => {
      mockUseBackends.mockReturnValue({
        backends: [
          {
            name: "codex",
            displayName: "Codex",
            available: false,
            installed: true,
          },
          {
            name: "claude",
            displayName: "Claude",
            available: true,
            installed: true,
          },
        ],
        isLoading: false,
        error: null,
      });
      renderModal({
        defaultName: "blocked-workflow",
        defaultBackend: "claude",
        repos: workflowRepos,
      });
      fireEvent.click(screen.getByTestId(templateTestID));

      expect(screen.getByTestId("create-agent-submit")).toBeDisabled();
      fireEvent.submit(document.getElementById("create-agent-form")!);
      expect(await screen.findByRole("alert")).toHaveTextContent(
        /codex is unavailable or not installed/i,
      );
      expect(mockCreateBinding).not.toHaveBeenCalled();
      expect(mockUpdateBinding).not.toHaveBeenCalled();
      expect(mockSetEnabled).not.toHaveBeenCalled();
      expect(mockEnsureConnector).not.toHaveBeenCalled();
      expect(mockAddGrant).not.toHaveBeenCalled();
      expect(mockReplaceGrants).not.toHaveBeenCalled();
      expect(mockCreatePromptAgentRecord).not.toHaveBeenCalled();
      expect(mockCreateAgent).not.toHaveBeenCalled();
    },
  );

  it("blocks bug-fix when the workspace/default backend is unhealthy without pinning its input", async () => {
    mockUseBackends.mockReturnValue({
      backends: [
        {
          name: "codex",
          displayName: "Codex",
          available: true,
          installed: true,
        },
        {
          name: "claude",
          displayName: "Claude",
          available: false,
          installed: true,
        },
      ],
      isLoading: false,
      error: null,
    });
    renderModal({
      defaultName: "bug-fix",
      defaultBackend: "claude",
      repos: workflowRepos,
    });
    fireEvent.click(screen.getByTestId("create-agent-template-bug-fix"));

    expect(screen.getByTestId("create-agent-submit")).toBeDisabled();
    fireEvent.submit(document.getElementById("create-agent-form")!);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /claude is unavailable or not installed/i,
    );
    expect(mockCreateBinding).not.toHaveBeenCalled();
    expect(mockUpdateBinding).not.toHaveBeenCalled();
    expect(mockSetEnabled).not.toHaveBeenCalled();
    expect(mockEnsureConnector).not.toHaveBeenCalled();
    expect(mockAddGrant).not.toHaveBeenCalled();
    expect(mockReplaceGrants).not.toHaveBeenCalled();
  });

  it("blocks a GitHub workflow before creating a binding when the target has no GitHub remote", async () => {
    const localOnlyRepos: RepoInfo[] = [
      {
        name: "local-only",
        path: "/local-only",
        default_branch: "main",
        remote: "/tmp/local-only.git",
        groups: [],
      },
    ];
    renderModal({
      defaultName: "bug-fix",
      repos: localOnlyRepos,
    });
    fireEvent.click(screen.getByTestId("create-agent-template-bug-fix"));
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /select a target repo with a github remote/i,
    );
    expect(mockCreateBinding).not.toHaveBeenCalled();
    expect(mockUpdateBinding).not.toHaveBeenCalled();
    expect(mockSetEnabled).not.toHaveBeenCalled();
  });

  it("activates local review for one local target without GitHub provisioning", async () => {
    mockUseLocalSettings.mockReturnValue({
      settings: { runtime_credentials: { github: { configured: false } } },
    });
    const localOnlyRepos: RepoInfo[] = [
      {
        name: "local-only",
        path: "/local-only",
        default_branch: "main",
        remote: "/tmp/local-only.git",
        groups: [],
      },
    ];
    renderModal({
      defaultName: "local-review",
      repos: localOnlyRepos,
    });
    fireEvent.click(screen.getByTestId("create-agent-template-local-review"));
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));

    await waitFor(() => expect(mockSetEnabled).toHaveBeenCalledTimes(2));
    expect(mockCreateBinding).toHaveBeenCalledWith(
      expect.objectContaining({
        workflow: "local-review-agent",
        binding_id: "s3-local-review",
        run_input: { targetRepo: "local-only" },
        enabled: false,
      }),
    );
    expect(mockUpdateBinding).toHaveBeenCalledWith(
      "s3-local-review",
      expect.objectContaining({
        run_input: { targetRepo: "local-only" },
      }),
    );
    expect(mockEnsureConnector).not.toHaveBeenCalled();
    expect(mockAddGrant).not.toHaveBeenCalled();
    expect(mockReplaceGrants).not.toHaveBeenCalled();
    expect(mockSetEnabled).toHaveBeenNthCalledWith(2, "s3-local-review", true);
  });
});

// ---------- error surfacing ----------

describe("CreateAgentModal: error handling", () => {
  it("surfaces ApiError messages from the backend", async () => {
    mockCreatePromptAgentRecord.mockRejectedValueOnce(
      new ApiError("conflict: agent already exists", 409),
    );
    renderModal({ defaultName: "dup" });
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /conflict: agent already exists/i,
    );
  });

  it("surfaces generic Error messages", async () => {
    mockCreatePromptAgentRecord.mockRejectedValueOnce(
      new Error("network down"),
    );
    renderModal({ defaultName: "x" });
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/network down/i);
  });

  it("falls back to a generic message for non-Error throws", async () => {
    mockCreatePromptAgentRecord.mockRejectedValueOnce("oops");
    renderModal({ defaultName: "x" });
    fireEvent.click(screen.getByRole("button", { name: /activate/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /failed to create agent/i,
    );
  });
});

// ---------- close affordances ----------

describe("CreateAgentModal: close affordances", () => {
  it("invokes onClose when the cancel button is clicked", () => {
    const { onClose } = renderModal();
    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("invokes onClose when the close (x) button is clicked", () => {
    const { onClose } = renderModal();
    fireEvent.click(screen.getByRole("button", { name: /^close$/i }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not invoke onClose when clicking near the dialog in the dismiss buffer", () => {
    const { onClose } = renderModal();
    const dialog = screen.getByRole("dialog");
    fireEvent.click(dialog.parentElement!);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("invokes onClose when clicking the backdrop outside the dismiss buffer", () => {
    const { onClose } = renderModal();
    const overlay = screen.getByTestId("create-agent-overlay");
    fireEvent.click(overlay, { target: overlay, currentTarget: overlay });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
