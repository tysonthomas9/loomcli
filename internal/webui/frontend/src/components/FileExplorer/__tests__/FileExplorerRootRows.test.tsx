/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { FileBrowserTab } from "@/stores";

import { FileExplorerTreePanel } from "../FileExplorerTreePanel";
import type { FileTreeSection } from "../treeRoots";

vi.mock("../skills", () => ({
  SkillsTreeBlock: () => <div role="tree" aria-label="Skills tree" />,
}));

const checkoutSection: FileTreeSection = {
  id: "repos",
  title: "Repos",
  roots: [
    {
      id: "repo:loomcli:",
      kind: "checkout",
      ref: { scope: "repo", target: "loomcli" },
      label: "loomcli",
      icon: "repo",
      exists: false,
      changeCount: 0,
      gitStatusUnavailable: false,
    },
  ],
};

const skillsSection: FileTreeSection = {
  id: "skills",
  title: "Skills",
  roots: [
    {
      id: "skills:workspace:",
      kind: "skills",
      ref: { kind: "skills", group: { kind: "workspace" } },
      label: "Workspace",
      secondary: "1 skill",
      skillCount: 1,
    },
  ],
};

function panel(sections: FileTreeSection[], expandedRoots: Set<string>) {
  return (
    <FileExplorerTreePanel
      workspaceId="ws-1"
      hasCheckouts={true}
      lens="files"
      changeCount={0}
      compareMode="branch"
      branchChangeCount={0}
      workingChangeCount={0}
      checkoutError={null}
      repairError={null}
      sections={sections}
      changeGroups={[]}
      unavailableCheckoutLabels={[]}
      expandedRoots={expandedRoots}
      repairingCheckoutKey={null}
      canWrite={true}
      canEditSkills={vi.fn(() => true)}
      selectedTab={null as FileBrowserTab | null}
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
      onSkillGroupContextMenu={vi.fn()}
      onNewSkill={vi.fn()}
      onOpenFile={vi.fn()}
      onContextMenu={vi.fn()}
      onRequestRename={vi.fn()}
      onRequestDelete={vi.fn()}
      onInlineEditChange={vi.fn()}
      onInlineEditCommit={vi.fn()}
      onInlineEditCancel={vi.fn()}
    />
  );
}

describe("FileExplorer root rows", () => {
  it("exposes and updates aria-expanded on checkout root rows", () => {
    const { rerender } = render(panel([checkoutSection], new Set()));
    const toggle = screen.getByRole("button", { name: "loomcli" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");

    rerender(panel([checkoutSection], new Set(["repo:loomcli:"])));
    expect(toggle).toHaveAttribute("aria-expanded", "true");
  });

  it("exposes and updates aria-expanded on skills root rows", () => {
    const { rerender } = render(panel([skillsSection], new Set()));
    const toggle = screen.getByRole("button", { name: "Workspace· 1 skill" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");

    rerender(panel([skillsSection], new Set(["skills:workspace:"])));
    expect(toggle).toHaveAttribute("aria-expanded", "true");
  });
});
