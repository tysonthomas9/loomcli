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
import { MemoryRouter } from "react-router-dom";
import "@testing-library/jest-dom";

import { CreateAgentModal } from "../CreateAgentModal";
import { ApiError } from "@/types/common";
import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";

// ---------- Mocks ----------

// useCreateWorkspaceAgent returns a function (request) => Promise<agent>.
// Tests swap the function per case via mockCreateAgent.mockImplementation.
const mockCreateAgent = vi.fn();
const mockEnsureRole = vi.fn();
const mockCreatePromptAgentRecord = vi.fn();
const mockListWorkspaceRoles = vi.fn();
const mockCreateBinding = vi.fn();
const mockEnsureConnector = vi.fn();
const mockAddGrant = vi.fn();

vi.mock("@/hooks/agents", () => ({
  useCreateWorkspaceAgent: () => mockCreateAgent,
  useEnsureWorkspaceRole: () => mockEnsureRole,
}));
vi.mock("@/api/agents", () => ({
  createPromptAgentRecord: (...args: unknown[]) =>
    mockCreatePromptAgentRecord(...args),
}));
vi.mock("@/api/workspace", () => ({
  listWorkspaceRoles: (...args: unknown[]) => mockListWorkspaceRoles(...args),
}));
vi.mock("@/hooks/workspace", () => ({
  GITHUB_CONNECTOR_ID: "github",
  dispatchBindingsChanged: vi.fn(),
  useAutomations: () => ({ createBinding: mockCreateBinding }),
  useBackends: () => ({
    backends: [
      { name: "codex", displayName: "codex" },
      { name: "claude", displayName: "claude" },
    ],
  }),
  useConnectorProvisioning: () => ({
    ensureConnector: mockEnsureConnector,
    addGrant: mockAddGrant,
  }),
  useLocalSettings: () => ({
    settings: { runtime_credentials: { github: { configured: true } } },
  }),
}));

// ---------- Helpers ----------

const repos: RepoInfo[] = [
  { name: "alpha", default_branch: "main", local_path: "/a" },
  { name: "beta", default_branch: "main", local_path: "/b" },
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
    </MemoryRouter>,
  );
  return { ...utils, onClose, onSuccess };
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
  mockEnsureConnector.mockReset();
  mockAddGrant.mockReset();
  mockListWorkspaceRoles.mockReset();
  mockEnsureRole.mockReset();
  mockCreatePromptAgentRecord.mockResolvedValue(sampleAgentRecord);
  mockListWorkspaceRoles.mockResolvedValue([]);
  mockEnsureRole.mockResolvedValue({
    name: "bug-triage",
    workspace_key: "ws-1",
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

  it("treats a workspace with no repos as workspace scope (cross_repo)", async () => {
    // With no repos available there are no chips to pick, so the agent is
    // created with workspace scope rather than erroring.
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "agent-x", repos: [] });
    fireEvent.click(screen.getByTestId("create-agent-template-legacy-task"));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      cross_repo: true,
      repos: [],
    });
  });
});

// ---------- happy-path submission ----------

describe("CreateAgentModal: submission", () => {
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
  });

  // (The "omit backend when empty" case is unreachable now that AI Backend is a
  // required dropdown — it always carries a value — so that test was removed.)

  it("sends cross_repo with empty repos when every repo chip is deselected", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "global" });
    fireEvent.click(screen.getByTestId("create-agent-template-legacy-task"));
    // The first repo ("alpha") is selected by default — deselect it so nothing
    // is picked, which maps to workspace scope.
    fireEvent.click(screen.getByRole("button", { name: /alpha/i }));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      cross_repo: true,
      repos: [],
    });
  });

  it("sends every selected repo (multi-repo agent)", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "spanner" });
    fireEvent.click(screen.getByTestId("create-agent-template-legacy-task"));
    // "alpha" is pre-selected; add "beta" so the agent spans both repos.
    fireEvent.click(screen.getByRole("button", { name: /beta/i }));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      cross_repo: false,
      repos: ["alpha", "beta"],
    });
  });

  it("submits lead agent when Lead template is selected", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "lead-nova" });
    fireEvent.click(screen.getByTestId("create-agent-template-lead"));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      name: "lead-nova",
      role_name: "lead",
    });
  });

  it("ensures the custom role before creating a custom-role agent", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "triage-1" });
    fireEvent.click(screen.getByTestId("create-agent-template-bug-triage"));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));

    // The role (+ its prompt) is provisioned first...
    await waitFor(() => expect(mockEnsureRole).toHaveBeenCalledTimes(1));
    expect(mockEnsureRole.mock.calls[0][0]).toMatchObject({
      name: "bug-triage",
      task_filter: "any",
      read_only: true,
    });
    // ...then the agent is created referencing that role.
    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      name: "triage-1",
      role_name: "bug-triage",
    });
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
