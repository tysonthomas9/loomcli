// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { RolePromptDTO, RoleSourceKind } from "@/api/roles";
import { ApiError } from "@/types/common";

import { RolePromptCard } from "../RolePromptCard";

const mocks = vi.hoisted(() => ({
  getRole: vi.fn(),
  updateRolePrompt: vi.fn(),
}));

vi.mock("@/api/roles", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/roles")>();
  return {
    ...actual,
    getRole: mocks.getRole,
    updateRolePrompt: mocks.updateRolePrompt,
  };
});

function detail(
  sourceKind: RoleSourceKind,
  editable: boolean,
  overrides: Partial<RolePromptDTO> = {},
): RolePromptDTO {
  return {
    role: {
      name: "worker",
      description: "Test role",
      kind: sourceKind === "inline" ? "interactive" : "worker",
      sourceKind,
      editable,
      editableReason: editable ? "" : "builtin",
      updatedAt: "2026-08-14T12:00:00Z",
    },
    sourceKind,
    sourceBody: "original prompt",
    editable,
    editableReason: editable ? "" : "builtin",
    revision: "2026-08-14T12:00:00Z",
    activationNote: editable
      ? "Takes effect when the daemon reconciles (~30s) or on next spawn; running agents keep the prompt they launched with."
      : "Read-only source.",
    ...overrides,
  };
}

describe("RolePromptCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getRole.mockResolvedValue(detail("inline", true));
    mocks.updateRolePrompt.mockResolvedValue(
      detail("inline", true, {
        sourceBody: "updated prompt",
        revision: "2026-08-14T12:01:00Z",
      }),
    );
  });

  it.each([
    ["builtinTemplate", false, "Built-in template — read-only"],
    ["managed", false, "Managed — read-only"],
    ["file", true, "File-backed prompt"],
    ["inline", true, "Inline prompt"],
    ["builtinSelector", true, "Built-in selector — editable override"],
  ] as const)(
    "renders %s with the correct editability",
    async (sourceKind, editable, label) => {
      mocks.getRole.mockResolvedValue(
        detail(sourceKind, editable, {
          editableReason: editable
            ? ""
            : sourceKind === "managed"
              ? "managed"
              : "builtin",
        }),
      );
      render(<RolePromptCard workspaceId="WS" roleName="worker" />);

      expect(await screen.findByText(label)).toBeInTheDocument();
      if (editable) {
        expect(screen.getByTestId("role-prompt-editor")).toHaveValue(
          "original prompt",
        );
      } else {
        expect(screen.getByTestId("role-prompt-source")).toHaveTextContent(
          "original prompt",
        );
        expect(
          screen.queryByRole("button", { name: "Save prompt" }),
        ).not.toBeInTheDocument();
      }
    },
  );

  it("dirty-gates Save and sends only prompt plus expectedRevision", async () => {
    render(<RolePromptCard workspaceId="WS" roleName="worker" />);
    const editor = await screen.findByTestId("role-prompt-editor");
    const save = screen.getByRole("button", { name: "Save prompt" });
    expect(save).toBeDisabled();

    fireEvent.change(editor, { target: { value: "updated prompt" } });
    expect(save).toBeEnabled();
    fireEvent.click(save);

    await waitFor(() => {
      expect(mocks.updateRolePrompt).toHaveBeenCalledWith("WS", "worker", {
        prompt: "updated prompt",
        expectedRevision: "2026-08-14T12:00:00Z",
      });
    });
    expect(editor).toHaveValue("updated prompt");
    expect(save).toBeDisabled();
  });

  it("preserves the draft and shows a non-destructive stale-revision state", async () => {
    mocks.updateRolePrompt.mockRejectedValue(
      new ApiError(409, "Conflict", {
        error: "role prompt changed elsewhere",
        code: "stale_revision",
      }),
    );
    render(<RolePromptCard workspaceId="WS" roleName="worker" />);
    const editor = await screen.findByTestId("role-prompt-editor");
    fireEvent.change(editor, { target: { value: "my unsaved draft" } });
    fireEvent.click(screen.getByRole("button", { name: "Save prompt" }));

    expect(
      await screen.findByText(
        "Prompt changed elsewhere — reload to pick up the latest.",
      ),
    ).toBeInTheDocument();
    expect(editor).toHaveValue("my unsaved draft");
  });

  it("surfaces load and unreadable-source errors instead of showing an empty prompt", async () => {
    mocks.getRole.mockRejectedValueOnce(
      new ApiError(500, "Internal Server Error", {
        error: "role prompt unavailable",
      }),
    );
    const { unmount } = render(
      <RolePromptCard workspaceId="WS" roleName="worker" />,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "role prompt unavailable",
    );
    expect(screen.queryByTestId("role-prompt-editor")).not.toBeInTheDocument();
    unmount();

    mocks.getRole.mockResolvedValue(
      detail("file", false, {
        sourceBody: "",
        sourceError: "Prompt file could not be read.",
        editableReason: "unreadable",
      }),
    );
    render(<RolePromptCard workspaceId="WS" roleName="worker" />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Prompt file could not be read.",
    );
    expect(screen.getByTestId("role-prompt-source")).toBeEmptyDOMElement();
  });
});
