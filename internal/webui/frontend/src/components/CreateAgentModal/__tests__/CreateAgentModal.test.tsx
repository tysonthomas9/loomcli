/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CreateAgentModal.
 *
 * Covers the v5-era validation surface plus the newer defaults behavior
 * introduced with onboarding (defaultName / defaultRoleName props +
 * wasOpenRef gating so the form doesn't reset state on every parent
 * re-render while the modal is open).
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { CreateAgentModal } from "../CreateAgentModal";
import { ApiError } from "@/types/common";
import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";

// ---------- Mocks ----------

// useCreateWorkspaceAgent returns a function (request) => Promise<agent>.
// Tests swap the function per case via mockCreateAgent.mockImplementation.
const mockCreateAgent = vi.fn();
vi.mock("@/hooks/agents", () => ({
  useCreateWorkspaceAgent: () => mockCreateAgent,
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

function renderModal(
  overrides: Partial<React.ComponentProps<typeof CreateAgentModal>> = {},
) {
  const onClose = vi.fn();
  const onSuccess = vi.fn();
  const utils = render(
    <CreateAgentModal
      isOpen
      workspaceId="ws-1"
      repos={repos}
      onClose={onClose}
      onSuccess={onSuccess}
      {...overrides}
    />,
  );
  return { ...utils, onClose, onSuccess };
}

beforeEach(() => {
  mockCreateAgent.mockReset();
});

// ---------- isOpen gate ----------

describe("CreateAgentModal: open/close gate", () => {
  it("renders nothing when isOpen is false", () => {
    const { container } = render(
      <CreateAgentModal
        isOpen={false}
        workspaceId="ws-1"
        repos={repos}
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
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
    expect(screen.getByTestId("create-agent-name")).toHaveValue("starter-agent");
  });

  it("trims whitespace in defaultName", () => {
    renderModal({ defaultName: "  pad  " });
    expect(screen.getByTestId("create-agent-name")).toHaveValue("pad");
  });

  it("seeds the role segmented control from defaultRoleName", () => {
    renderModal({ defaultRoleName: "plan" });
    expect(screen.getByRole("button", { name: "Plan" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("defaults role to 'task' when defaultRoleName is omitted", () => {
    renderModal();
    expect(screen.getByRole("button", { name: "Task" })).toHaveAttribute(
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
      <CreateAgentModal
        isOpen
        workspaceId="ws-1"
        repos={repos}
        defaultName="seeded"
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );
    const nameInput = screen.getByTestId("create-agent-name");
    expect(nameInput).toHaveValue("seeded");

    // User edits the name.
    fireEvent.change(nameInput, { target: { value: "my-custom-name" } });
    expect(nameInput).toHaveValue("my-custom-name");

    // Parent re-renders (e.g., a sibling state changed) with same props.
    rerender(
      <CreateAgentModal
        isOpen
        workspaceId="ws-1"
        repos={repos}
        defaultName="seeded"
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    // The wasOpenRef gate must prevent the effect from re-seeding.
    expect(screen.getByTestId("create-agent-name")).toHaveValue("my-custom-name");
  });

  it("re-seeds defaults on close → open transition", () => {
    const { rerender } = render(
      <CreateAgentModal
        isOpen
        workspaceId="ws-1"
        repos={repos}
        defaultName="first-default"
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );
    // User edits.
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "user-typed" },
    });
    // Close.
    rerender(
      <CreateAgentModal
        isOpen={false}
        workspaceId="ws-1"
        repos={repos}
        defaultName="first-default"
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );
    // Re-open with a NEW default — should seed the new value.
    rerender(
      <CreateAgentModal
        isOpen
        workspaceId="ws-1"
        repos={repos}
        defaultName="second-default"
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );
    expect(screen.getByTestId("create-agent-name")).toHaveValue("second-default");
  });
});

// ---------- validation ----------

describe("CreateAgentModal: client-side validation", () => {
  it("disables Create until a name is entered (does not call API)", () => {
    const { onSuccess } = renderModal();
    expect(
      screen.getByRole("button", { name: /create agent/i }),
    ).toBeDisabled();
    expect(mockCreateAgent).not.toHaveBeenCalled();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it("keeps Create disabled for a whitespace-only name", () => {
    renderModal();
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "   " },
    });
    expect(
      screen.getByRole("button", { name: /create agent/i }),
    ).toBeDisabled();
    expect(mockCreateAgent).not.toHaveBeenCalled();
  });

  it("treats a workspace with no repos as workspace scope (cross_repo)", async () => {
    // With no repos available there are no chips to pick, so the agent is
    // created with workspace scope rather than erroring.
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "agent-x", repos: [] });
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
  it("submits repo-scoped agent with trimmed values + invokes onSuccess", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    const { onSuccess } = renderModal({
      defaultName: "  planner  ",
      defaultBackend: " claude ",
    });
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));

    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalledTimes(1));
    expect(mockCreateAgent).toHaveBeenCalledWith({
      name: "planner",
      role_name: "task",
      auto: false,
      cross_repo: false,
      repos: ["alpha"], // first repo is the default
      backend: "claude",
    });
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(sampleAgent));
  });

  // (The "omit backend when empty" case is unreachable now that AI Backend is a
  // required dropdown — it always carries a value — so that test was removed.)

  it("sends cross_repo with empty repos when every repo chip is deselected", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "global" });
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
    // "alpha" is pre-selected; add "beta" so the agent spans both repos.
    fireEvent.click(screen.getByRole("button", { name: /beta/i }));
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    expect(mockCreateAgent.mock.calls[0][0]).toMatchObject({
      cross_repo: false,
      repos: ["alpha", "beta"],
    });
  });

  it("resets the form to the configured defaults after a successful submit", async () => {
    mockCreateAgent.mockResolvedValueOnce(sampleAgent);
    renderModal({ defaultName: "seed-name", defaultRoleName: "plan" });

    // User overrides the name.
    fireEvent.change(screen.getByTestId("create-agent-name"), {
      target: { value: "one-off" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));

    await waitFor(() => expect(mockCreateAgent).toHaveBeenCalled());
    // After success the form returns to the defaults the parent supplied,
    // not the prior v5-era hard-coded blank/"task".
    await waitFor(() => {
      expect(screen.getByTestId("create-agent-name")).toHaveValue("seed-name");
    });
    expect(screen.getByRole("button", { name: "Plan" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });
});

// ---------- error surfacing ----------

describe("CreateAgentModal: error handling", () => {
  it("surfaces ApiError messages from the backend", async () => {
    mockCreateAgent.mockRejectedValueOnce(
      new ApiError("conflict: agent already exists", 409),
    );
    renderModal({ defaultName: "dup" });
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /conflict: agent already exists/i,
    );
  });

  it("surfaces generic Error messages", async () => {
    mockCreateAgent.mockRejectedValueOnce(new Error("network down"));
    renderModal({ defaultName: "x" });
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/network down/i);
  });

  it("falls back to a generic message for non-Error throws", async () => {
    mockCreateAgent.mockRejectedValueOnce("oops");
    renderModal({ defaultName: "x" });
    fireEvent.click(screen.getByRole("button", { name: /create agent/i }));
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
});
