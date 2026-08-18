/** @vitest-environment jsdom */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentServiceDTO } from "@/api/agentServices";
import { ApiError } from "@/types/common";

import { CreateAgentServiceModal } from "../CreateAgentServiceModal";

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
}));

vi.mock("@/hooks/workspace", () => ({
  useInstantiableScriptedRoles: () => ({
    roles: [
      {
        roleName: "scout",
        displayName: "Scout",
      },
    ],
    loading: false,
    initialized: true,
    error: null,
    refresh: vi.fn(),
  }),
  useAgentServiceMutations: () => ({
    create: mocks.create,
    patch: vi.fn(),
    remove: vi.fn(),
  }),
}));

const createdService: AgentServiceDTO = {
  id: "scout-west",
  name: "Scout West",
  triggerKind: "cron",
  enabled: true,
  behavior: {
    roleName: "scout",
    roleDisplayName: "Scout",
    scripted: true,
  },
  bindings: [],
  nextFireAt: null,
  lastRunStatus: "",
  consecutiveFailures: 0,
  errors: [],
  createdAt: "2026-08-15T00:00:00Z",
  updatedAt: "2026-08-15T00:00:00Z",
};

function renderModal(overrides: { onClose?: () => void } = {}) {
  const onClose = overrides.onClose ?? vi.fn();
  const onSuccess = vi.fn();
  render(
    <CreateAgentServiceModal
      isOpen
      workspaceId="WS"
      onClose={onClose}
      onSuccess={onSuccess}
    />,
  );
  return { onClose, onSuccess };
}

function completeRequiredFields(): void {
  fireEvent.change(screen.getByLabelText("Instance ID"), {
    target: { value: "scout-west" },
  });
  fireEvent.click(screen.getByRole("button", { name: "@daily" }));
}

describe("CreateAgentServiceModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.create.mockResolvedValue(createdService);
  });

  it("validates the service ID grammar live and requires a schedule", () => {
    renderModal();

    const id = screen.getByLabelText("Instance ID");
    fireEvent.change(id, { target: { value: "Scout_West" } });
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Use 1–64 lowercase letters, numbers, or hyphens",
    );

    fireEvent.change(id, { target: { value: "scout-west" } });
    expect(
      screen.queryByText(/Use 1–64 lowercase letters/),
    ).not.toBeInTheDocument();
    fireEvent.blur(screen.getByLabelText("Schedule"));
    expect(screen.getByRole("alert")).toHaveTextContent("Schedule is required");
    expect(screen.getByRole("button", { name: "Add agent" })).toBeDisabled();
  });

  it("submits the selected role and navigable created service", async () => {
    const { onClose, onSuccess } = renderModal();
    completeRequiredFields();
    fireEvent.change(screen.getByLabelText("Display name (optional)"), {
      target: { value: "Scout West" },
    });
    fireEvent.change(screen.getByLabelText("Timezone (optional)"), {
      target: { value: "America/Los_Angeles" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Add agent" }));

    await waitFor(() => {
      expect(mocks.create).toHaveBeenCalledWith({
        id: "scout-west",
        name: "Scout West",
        role: "scout",
        binding: {
          schedule: "@daily",
          timezone: "America/Los_Angeles",
        },
      });
    });
    expect(onSuccess).toHaveBeenCalledWith(createdService);
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("surfaces a duplicate conflict inline", async () => {
    mocks.create.mockRejectedValueOnce(
      new ApiError(409, "Conflict", {
        error: "domain: already exists",
      }),
    );
    renderModal();
    completeRequiredFields();

    fireEvent.click(screen.getByRole("button", { name: "Add agent" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      'An agent with ID "scout-west" already exists.',
    );

    fireEvent.change(screen.getByLabelText("Instance ID"), {
      target: { value: "scout-east" },
    });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("turns FileAccess rejection into the local-frontend guidance", async () => {
    mocks.create.mockRejectedValueOnce(
      new ApiError(403, "Forbidden", { error: "forbidden" }),
    );
    renderModal();
    completeRequiredFields();

    fireEvent.click(screen.getByRole("button", { name: "Add agent" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "workspace file write access",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "configured local frontend",
    );
  });
});
