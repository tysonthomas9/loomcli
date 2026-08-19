/**
 * @vitest-environment jsdom
 */

import "@testing-library/jest-dom";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { CheckoutRef } from "@/utils/fileExplorerRefs";
import type { FileBrowserTab } from "@/stores";

import { FileExplorerTreePanel } from "../FileExplorerTreePanel";
import type { ChangeCheckoutGroup } from "../changesLens";
import type { HistoryOpenDiffRequest } from "../FileHistoryPanel";
import type { FileTreeSection } from "../treeRoots";
import type { CompareMode, ExplorerLens } from "../workspaceFileBrowserTypes";

const agentRef: CheckoutRef = {
  scope: "agent",
  target: "atlas",
  repo: "loomcli",
};

function changeGroup(
  ref: CheckoutRef = agentRef,
  overrides: Partial<ChangeCheckoutGroup> = {},
): ChangeCheckoutGroup {
  return {
    id: "agent:atlas:loomcli",
    ref,
    label: "atlas · loomcli · 1",
    changeCount: 1,
    loaded: true,
    items: [
      {
        path: "src/main.ts",
        name: "main.ts",
        parentPath: "src",
        status: { kind: "modified", label: "Modified" },
        additions: 2,
        deletions: 1,
      },
    ],
    ...overrides,
  };
}

function repoSection(changeCount: number): FileTreeSection {
  return {
    id: "repos",
    title: "Repos",
    roots: [
      {
        id: "repo:loomcli",
        kind: "checkout",
        ref: { scope: "repo", target: "loomcli" },
        label: "loomcli",
        icon: "repo",
        exists: true,
        changeCount,
        gitStatusUnavailable: false,
      },
    ],
  };
}

function renderPanel(
  overrides: Partial<{
    lens: ExplorerLens;
    compareMode: CompareMode;
    changeGroups: ChangeCheckoutGroup[];
    sections: FileTreeSection[];
    branchChangeCount: number;
    taskChangeCount: number;
    workingChangeCount: number;
    showTaskCompareMode: boolean;
    branchBaseName: string;
    onCompareModeChange: (compareMode: CompareMode) => void;
    onOpenDiff: (request: HistoryOpenDiffRequest) => void;
  }> = {},
) {
  return render(
    <FileExplorerTreePanel
      lens={overrides.lens ?? "changes"}
      changeCount={0}
      compareMode={overrides.compareMode ?? "branch"}
      branchChangeCount={overrides.branchChangeCount ?? 2}
      taskChangeCount={overrides.taskChangeCount ?? 0}
      workingChangeCount={overrides.workingChangeCount ?? 3}
      branchBaseName={overrides.branchBaseName}
      showTaskCompareMode={overrides.showTaskCompareMode ?? false}
      checkoutError={null}
      repairError={null}
      sections={overrides.sections ?? []}
      changeGroups={overrides.changeGroups ?? []}
      unavailableCheckoutLabels={[]}
      expandedRoots={new Set()}
      repairingCheckoutKey={null}
      canWrite={true}
      selectedTab={null as FileBrowserTab | null}
      inlineEdit={null}
      gitStatusByRef={{}}
      treeRevealRequests={{}}
      treeRefreshRequests={{}}
      hideAgentSectionHeading={false}
      onLensChange={vi.fn()}
      onCompareModeChange={overrides.onCompareModeChange ?? vi.fn()}
      onQuickOpen={vi.fn()}
      onOpenDiff={overrides.onOpenDiff ?? vi.fn()}
      onToggleRoot={vi.fn()}
      onRepairCheckout={vi.fn()}
      onCheckoutContextMenu={vi.fn()}
      onOpenFile={vi.fn()}
      onContextMenu={vi.fn()}
      onRequestRename={vi.fn()}
      onRequestDelete={vi.fn()}
      onInlineEditChange={vi.fn()}
      onInlineEditCommit={vi.fn()}
      onInlineEditCancel={vi.fn()}
    />,
  );
}

describe("FileExplorerTreePanel", () => {
  it("renders compare mode counts only in the Changes lens", () => {
    const { rerender } = renderPanel({
      branchChangeCount: 4,
      workingChangeCount: 7,
    });

    expect(
      screen.getByRole("tablist", { name: "Compare mode" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "Branch vs base · 4" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "Working tree · 7" }),
    ).toBeInTheDocument();

    rerender(
      <FileExplorerTreePanel
        lens="files"
        changeCount={7}
        compareMode="branch"
        branchChangeCount={4}
        taskChangeCount={0}
        workingChangeCount={7}
        showTaskCompareMode={false}
        checkoutError={null}
        repairError={null}
        sections={[]}
        changeGroups={[]}
        unavailableCheckoutLabels={[]}
        expandedRoots={new Set()}
        repairingCheckoutKey={null}
        canWrite={true}
        selectedTab={null}
        inlineEdit={null}
        gitStatusByRef={{}}
        treeRevealRequests={{}}
        treeRefreshRequests={{}}
        hideAgentSectionHeading={false}
        onLensChange={vi.fn()}
        onCompareModeChange={vi.fn()}
        onQuickOpen={vi.fn()}
        onOpenDiff={vi.fn()}
        onToggleRoot={vi.fn()}
        onRepairCheckout={vi.fn()}
        onCheckoutContextMenu={vi.fn()}
        onOpenFile={vi.fn()}
        onContextMenu={vi.fn()}
        onRequestRename={vi.fn()}
        onRequestDelete={vi.fn()}
        onInlineEditChange={vi.fn()}
        onInlineEditCommit={vi.fn()}
        onInlineEditCancel={vi.fn()}
      />,
    );

    expect(
      screen.queryByRole("tablist", { name: "Compare mode" }),
    ).not.toBeInTheDocument();
  });

  it("clicking the inactive compare segment changes mode", () => {
    const onCompareModeChange = vi.fn();
    renderPanel({ compareMode: "branch", onCompareModeChange });

    fireEvent.click(screen.getByRole("tab", { name: "Working tree · 3" }));

    expect(onCompareModeChange).toHaveBeenCalledWith("working");
  });

  it("renders and selects the task compare segment when available", () => {
    const onCompareModeChange = vi.fn();
    renderPanel({
      compareMode: "branch",
      taskChangeCount: 5,
      showTaskCompareMode: true,
      onCompareModeChange,
    });

    expect(screen.getByRole("tab", { name: "By task · 5" })).toHaveAttribute(
      "title",
      "Each task's committed increment: its stack base branch diffed against its output branch.",
    );

    fireEvent.click(screen.getByRole("tab", { name: "By task · 5" }));

    expect(onCompareModeChange).toHaveBeenCalledWith("tasks");
  });

  it("opens task rows with repo-scoped explicit refs", () => {
    const onOpenDiff = vi.fn();
    renderPanel({
      compareMode: "tasks",
      showTaskCompareMode: true,
      taskChangeCount: 1,
      changeGroups: [
        changeGroup(
          { scope: "repo", target: "loomcli" },
          {
            id: "epic:E/T1",
            label: "T1 · loomcli · 1",
            diffFrom: "main",
            diffTo: "task/T1",
            diffTitle: "T1",
          },
        ),
      ],
      onOpenDiff,
    });

    fireEvent.click(
      screen.getByRole("button", {
        name: "Open diff for src/main.ts (Modified)",
      }),
    );

    expect(onOpenDiff).toHaveBeenCalledWith({
      ref: { scope: "repo", target: "loomcli" },
      path: "src/main.ts",
      from: "main",
      to: "task/T1",
      title: "T1",
      canOpenFile: false,
    });
  });

  it("folds a known base name into the branch segment", () => {
    renderPanel({ compareMode: "branch", branchBaseName: "main" });

    expect(
      screen.getByRole("tab", { name: "Branch vs main · 2" }),
    ).toHaveAttribute(
      "title",
      "Committed changes on each agent branch, diffed against the merge-base with main.",
    );
  });

  it("uses the branch label and tooltip fallback when the base is unknown", () => {
    renderPanel({ compareMode: "branch" });

    expect(
      screen.getByRole("tab", { name: "Branch vs base · 2" }),
    ).toHaveAttribute(
      "title",
      "Committed changes on each agent branch, diffed against the merge-base with the repo default branch.",
    );
  });

  it("describes working-tree mode without a separate hint", () => {
    renderPanel({ compareMode: "working", branchBaseName: "main" });

    expect(
      screen.getByRole("tab", { name: "Working tree · 3" }),
    ).toHaveAttribute(
      "title",
      "Uncommitted changes in each checkout — like git status. Committed work does not appear here.",
    );
    expect(screen.queryByText("uncommitted only")).not.toBeInTheDocument();
  });

  it("opens branch diffs with source branch and no from revision", () => {
    const onOpenDiff = vi.fn();
    renderPanel({
      compareMode: "branch",
      changeGroups: [changeGroup()],
      onOpenDiff,
    });

    fireEvent.click(
      screen.getByRole("button", {
        name: "Open diff for src/main.ts (Modified)",
      }),
    );

    expect(onOpenDiff).toHaveBeenCalledWith({
      ref: agentRef,
      path: "src/main.ts",
      source: "branch",
      title: "atlas · loomcli",
      canOpenFile: true,
    });
    expect(onOpenDiff.mock.calls[0]?.[0]).not.toHaveProperty("from");
    expect(screen.getByText("+2")).toBeInTheDocument();
    expect(screen.getByText("−1")).toBeInTheDocument();
  });

  it("opens working-tree diffs from HEAD", () => {
    const onOpenDiff = vi.fn();
    renderPanel({
      compareMode: "working",
      changeGroups: [changeGroup()],
      onOpenDiff,
    });

    fireEvent.click(
      screen.getByRole("button", {
        name: "Open diff for src/main.ts (Modified)",
      }),
    );

    expect(onOpenDiff).toHaveBeenCalledWith({
      ref: agentRef,
      path: "src/main.ts",
      from: "HEAD",
      title: "atlas · loomcli",
      canOpenFile: true,
    });
    expect(onOpenDiff.mock.calls[0]?.[0]).not.toHaveProperty("source");
  });

  it("shows repo-scope changes notice in branch mode", () => {
    renderPanel({
      compareMode: "branch",
      sections: [repoSection(2)],
    });

    expect(
      screen.getByText("Shared checkout changes appear under Working tree."),
    ).toBeInTheDocument();
  });

  it("uses mode-specific empty states", () => {
    const { rerender } = renderPanel({ compareMode: "branch" });
    expect(
      screen.getByText("No committed changes vs base across this workspace."),
    ).toBeInTheDocument();

    rerender(
      <FileExplorerTreePanel
        lens="changes"
        changeCount={0}
        compareMode="working"
        branchChangeCount={0}
        taskChangeCount={0}
        workingChangeCount={0}
        showTaskCompareMode={false}
        checkoutError={null}
        repairError={null}
        sections={[]}
        changeGroups={[]}
        unavailableCheckoutLabels={[]}
        expandedRoots={new Set()}
        repairingCheckoutKey={null}
        canWrite={true}
        selectedTab={null}
        inlineEdit={null}
        gitStatusByRef={{}}
        treeRevealRequests={{}}
        treeRefreshRequests={{}}
        hideAgentSectionHeading={false}
        onLensChange={vi.fn()}
        onCompareModeChange={vi.fn()}
        onQuickOpen={vi.fn()}
        onOpenDiff={vi.fn()}
        onToggleRoot={vi.fn()}
        onRepairCheckout={vi.fn()}
        onCheckoutContextMenu={vi.fn()}
        onOpenFile={vi.fn()}
        onContextMenu={vi.fn()}
        onRequestRename={vi.fn()}
        onRequestDelete={vi.fn()}
        onInlineEditChange={vi.fn()}
        onInlineEditCommit={vi.fn()}
        onInlineEditCancel={vi.fn()}
      />,
    );

    expect(
      screen.getByText("No uncommitted changes across this workspace."),
    ).toBeInTheDocument();

    rerender(
      <FileExplorerTreePanel
        lens="changes"
        changeCount={0}
        compareMode="tasks"
        branchChangeCount={0}
        taskChangeCount={0}
        workingChangeCount={0}
        showTaskCompareMode
        checkoutError={null}
        repairError={null}
        sections={[]}
        changeGroups={[]}
        unavailableCheckoutLabels={[]}
        expandedRoots={new Set()}
        repairingCheckoutKey={null}
        canWrite={true}
        selectedTab={null}
        inlineEdit={null}
        gitStatusByRef={{}}
        treeRevealRequests={{}}
        treeRefreshRequests={{}}
        hideAgentSectionHeading={false}
        onLensChange={vi.fn()}
        onCompareModeChange={vi.fn()}
        onQuickOpen={vi.fn()}
        onOpenDiff={vi.fn()}
        onToggleRoot={vi.fn()}
        onRepairCheckout={vi.fn()}
        onCheckoutContextMenu={vi.fn()}
        onOpenFile={vi.fn()}
        onContextMenu={vi.fn()}
        onRequestRename={vi.fn()}
        onRequestDelete={vi.fn()}
        onInlineEditChange={vi.fn()}
        onInlineEditCommit={vi.fn()}
        onInlineEditCancel={vi.fn()}
      />,
    );

    expect(
      screen.getByText("No task increments found in this workspace's stacks."),
    ).toBeInTheDocument();
  });
});
