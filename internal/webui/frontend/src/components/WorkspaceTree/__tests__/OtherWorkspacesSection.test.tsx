/**
 * @vitest-environment jsdom
 */

import type { ReactNode } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { WorkspaceSummary } from "@/api/workspace";
import { OtherWorkspacesSection } from "../OtherWorkspacesSection";

const { mockReorderWorkspaces, mockShowToast } = vi.hoisted(() => ({
  mockReorderWorkspaces: vi.fn(),
  mockShowToast: vi.fn(),
}));

vi.mock("@/hooks/api", () => ({
  renameWorkspace: vi.fn(),
  deleteWorkspace: vi.fn(),
  reorderWorkspaces: (...args: unknown[]) => mockReorderWorkspaces(...args),
}));

vi.mock("@/hooks", () => ({
  useToast: () => ({ showToast: mockShowToast }),
  useRegisterEscapeLayer: vi.fn(),
  LAYER_CONFIRM_DIALOG: 60,
}));

vi.mock("@dnd-kit/core", () => ({
  DndContext: ({ children }: { children: ReactNode }) => <>{children}</>,
  closestCenter: vi.fn(),
  PointerSensor: vi.fn(),
  KeyboardSensor: vi.fn(),
  useSensor: vi.fn(() => ({})),
  useSensors: vi.fn(() => []),
}));

vi.mock("@dnd-kit/sortable", () => ({
  SortableContext: ({ children }: { children: ReactNode }) => <>{children}</>,
  verticalListSortingStrategy: {},
  arrayMove: <T,>(items: T[], from: number, to: number): T[] => {
    const next = items.slice();
    const [item] = next.splice(from, 1);
    if (item !== undefined) {
      next.splice(to, 0, item);
    }
    return next;
  },
  useSortable: () => ({
    attributes: {},
    listeners: {},
    setNodeRef: vi.fn(),
    transform: null,
    transition: null,
    isDragging: false,
  }),
}));

function workspace(id: string, name: string, active = false): WorkspaceSummary {
  return {
    id,
    name,
    path: `/workspaces/${id}`,
    active,
    repo_count: 0,
    is_default: false,
  };
}

function renderedWorkspaceNames(): string[] {
  return screen
    .getAllByLabelText(/^Switch to workspace /)
    .map((el) => el.textContent ?? "");
}

describe("OtherWorkspacesSection", () => {
  beforeEach(() => {
    mockReorderWorkspaces.mockReset();
    mockShowToast.mockReset();
  });

  it("reverts optimistic keyboard reorder and shows a toast when save fails", async () => {
    let rejectReorder: ((reason?: unknown) => void) | undefined;
    mockReorderWorkspaces.mockImplementation(
      () =>
        new Promise((_resolve, reject) => {
          rejectReorder = reject;
        }),
    );
    const refetchWorkspaces = vi.fn();

    render(
      <OtherWorkspacesSection
        workspaces={[
          workspace("ALPHA", "Alpha", true),
          workspace("BETA", "Beta"),
          workspace("GAMMA", "Gamma"),
        ]}
        activeWorkspaceName="Alpha"
        refetchWorkspaces={refetchWorkspaces}
      />,
    );

    expect(renderedWorkspaceNames()).toEqual(["Beta", "Gamma"]);

    const betaEntry = screen
      .getByLabelText("Switch to workspace Beta")
      .closest('[role="button"]');
    expect(betaEntry).not.toBeNull();
    fireEvent.keyDown(betaEntry as HTMLElement, {
      key: "ArrowDown",
      altKey: true,
    });

    expect(mockReorderWorkspaces).toHaveBeenCalledWith(["GAMMA", "BETA"]);
    expect(renderedWorkspaceNames()).toEqual(["Gamma", "Beta"]);

    await act(async () => {
      rejectReorder?.(new Error("Save failed"));
    });

    await waitFor(() => {
      expect(renderedWorkspaceNames()).toEqual(["Beta", "Gamma"]);
    });
    expect(mockShowToast).toHaveBeenCalledWith("Save failed", {
      type: "error",
    });
    expect(refetchWorkspaces).toHaveBeenCalledOnce();
  });
});
