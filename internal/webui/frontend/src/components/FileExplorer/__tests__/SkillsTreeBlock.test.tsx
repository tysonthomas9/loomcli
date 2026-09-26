/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useScopedFileTreeCore, useSkillsTree } from "@/hooks";
import type { SkillsExplorerRef } from "@/utils/explorerRefs";
import { SkillsTreeBlock } from "../skills";

vi.mock("@/hooks", () => ({
  useScopedFileTreeCore: vi.fn(),
  useSkillsTree: vi.fn(),
}));

const mockUseSkillsTree = vi.mocked(useSkillsTree);
const mockUseTree = vi.mocked(useScopedFileTreeCore);
const workspaceRef: SkillsExplorerRef = {
  kind: "skills",
  group: { kind: "workspace" },
};
const roleRef: SkillsExplorerRef = {
  kind: "skills",
  group: { kind: "role", role: "reviewer" },
};

function catalog(overrides: Record<string, unknown> = {}) {
  return {
    status: "loaded" as const,
    revision: 1,
    groups: [],
    error: null,
    shadowedByRef: {},
    shadowsByRef: {},
    readOnlyRefs: new Set<string>(),
    retry: vi.fn(),
    invalidate: vi.fn(),
    loader: vi.fn(),
    skills: [{ name: "audit" }],
    shadowed: new Set<string>(),
    shadows: new Set<string>(),
    ...overrides,
  };
}

function tree(entries = [{ name: "audit", is_dir: true, size: 0 }]) {
  return {
    expanded: new Set<string>(),
    treeData: new Map([["", entries]]),
    selectedPath: null,
    isLoading: false,
    error: null,
    filterText: "",
    debouncedFilterText: "",
    setFilterText: vi.fn(),
    toggle: vi.fn(),
    loadDir: vi.fn(),
    revealPath: vi.fn().mockResolvedValue(undefined),
  };
}

function renderBlock(
  refInfo: SkillsExplorerRef,
  canEdit: boolean,
  onNewSkill = vi.fn(),
) {
  return render(
    <SkillsTreeBlock
      workspaceId="ws-1"
      refInfo={refInfo}
      depthOffset={1}
      canEdit={canEdit}
      selectedTab={null}
      inlineEdit={null}
      onOpenFile={vi.fn()}
      onContextMenu={vi.fn()}
      onRequestDelete={vi.fn()}
      onNewSkill={onNewSkill}
      onInlineEditChange={vi.fn()}
      onInlineEditCommit={vi.fn()}
      onInlineEditCancel={vi.fn()}
    />,
  );
}

describe("SkillsTreeBlock", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseSkillsTree.mockReturnValue(catalog());
    mockUseTree.mockReturnValue(tree());
  });

  it("marks a shadowed workspace skill as overridden and dimmed", () => {
    mockUseSkillsTree.mockReturnValue(
      catalog({ shadowed: new Set(["audit"]) }),
    );
    renderBlock(workspaceRef, false);

    expect(screen.getByText("overridden")).toBeInTheDocument();
    expect(
      screen.getByText("audit").closest('[role="treeitem"]'),
    ).toHaveAttribute("data-shadowed", "true");
  });

  it("marks the role skill that overrides workspace", () => {
    mockUseSkillsTree.mockReturnValue(catalog({ shadows: new Set(["audit"]) }));
    renderBlock(roleRef, true);
    expect(screen.getByText("overrides workspace")).toBeInTheDocument();
  });

  it("renders empty role and workspace groups with scoped affordances", () => {
    mockUseSkillsTree.mockReturnValue(catalog({ skills: [] }));
    const onNewSkill = vi.fn();
    const { rerender } = renderBlock(workspaceRef, false, onNewSkill);
    const workspaceButton = screen.getByRole("button", { name: "New skill" });
    expect(workspaceButton).toBeDisabled();
    expect(workspaceButton).toHaveAttribute(
      "title",
      "Use `loom skill update` for workspace skills",
    );

    rerender(
      <SkillsTreeBlock
        workspaceId="ws-1"
        refInfo={roleRef}
        depthOffset={1}
        canEdit={true}
        selectedTab={null}
        inlineEdit={null}
        onOpenFile={vi.fn()}
        onContextMenu={vi.fn()}
        onRequestDelete={vi.fn()}
        onNewSkill={onNewSkill}
        onInlineEditChange={vi.fn()}
        onInlineEditCommit={vi.fn()}
        onInlineEditCancel={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "New skill" }));
    expect(onNewSkill).toHaveBeenCalledWith(roleRef);
  });

  it("shows the real catalog error and retries", () => {
    const retry = vi.fn();
    mockUseSkillsTree.mockReturnValue(
      catalog({ status: "error", error: "fleet-db is unavailable", retry }),
    );
    renderBlock(roleRef, true);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "fleet-db is unavailable",
    );
    expect(screen.queryByText("This checkout is not checked out")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(retry).toHaveBeenCalledOnce();
  });
});
