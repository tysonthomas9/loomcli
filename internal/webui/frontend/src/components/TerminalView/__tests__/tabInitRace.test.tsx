/**
 * @vitest-environment jsdom
 */

/**
 * Regression test for the same-commit stale read that dropped terminal tabs
 * when switching workspaces and back (PUPPET-13 / PUPPET-32).
 *
 * TerminalView remounts on a workspace switch with the Terminal view inactive.
 * When the user then opens Terminal, `isViewActive` and the metadata hook's
 * `enabled` flip in the SAME commit. With readiness derived from timing
 * ("nothing says we're loading"), useTabInit's effect still saw the previous
 * render's loading=false / tabs=[] and took the "empty workspace -> auto-create
 * a default tab" branch, clobbering the real tab list arriving milliseconds
 * later and minting a stray lead-{backend}-1 session.
 *
 * This harness wires useTerminalMetadata and useTabInit together exactly as
 * TerminalView does, and flips isActive false->true while the GET is still in
 * flight. It fails on the pre-fix tree.
 */

import { render, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { useRef, useState } from "react";

import { listTabMetadata, putTabMetadata } from "@/api/terminal";
import type { TabMetadata } from "@/api/terminal";
import type { BackendConfigData } from "@/api/common";

import { useTerminalMetadata } from "@/hooks/terminal/useTerminalMetadata";
import { useTabInit } from "../tabs/useTabInit";
import type { TabState } from "../tabs/terminalTabUtils";

vi.mock("@/api/terminal", () => ({
  listTabMetadata: vi.fn(),
  putTabMetadata: vi.fn(),
  patchTabMetadata: vi.fn(),
  deleteTabMetadata: vi.fn(),
}));

const mockList = vi.mocked(listTabMetadata);
const mockPut = vi.mocked(putTabMetadata);

const WORKSPACE = "uuid-A";

const CONFIG: BackendConfigData = {
  backend: "claude",
  source: "config",
  available: ["claude", "codex"],
  agents: [],
};

function makeMeta(sessionName: string, sortOrder: number): TabMetadata {
  return {
    session_name: sessionName,
    label: sessionName,
    notes: "",
    sort_order: sortOrder,
    pinned: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

/**
 * Minimal stand-in for TerminalView: same hook wiring, no terminal rendering.
 */
function Harness({ isActive }: { isActive: boolean }): JSX.Element {
  const [tabs, setTabs] = useState<TabState[]>([]);
  const [activeTabId, setActiveTabId] = useState("");
  const initializedRef = useRef(false);

  const {
    tabs: tabMetadata,
    createTab,
    loadedFor: metaLoadedFor,
    unavailable: metaUnavailable,
  } = useTerminalMetadata(WORKSPACE, { enabled: isActive });

  useTabInit({
    tabMetadata,
    metaReady: Boolean(WORKSPACE) && metaLoadedFor === WORKSPACE,
    metaUnavailable,
    config: CONFIG,
    configLoading: false,
    createTab,
    setTabs,
    setActiveTabId,
    initializedRef,
    workspace: WORKSPACE,
    isViewActive: isActive,
    excludeAgentTabs: true,
  });

  return (
    <div data-testid="tabs" data-active-tab={activeTabId}>
      {tabs.map((tab) => (
        <span key={tab.id} data-testid="tab">
          {tab.sessionName}
        </span>
      ))}
    </div>
  );
}

describe("terminal tab init race (workspace switch)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockPut.mockResolvedValue(makeMeta("ignored", 0));
  });

  it("does not auto-create a tab when the view activates with the tab list in flight", async () => {
    let resolveList!: (value: TabMetadata[]) => void;
    mockList.mockImplementation(
      () =>
        new Promise<TabMetadata[]>((resolve) => {
          resolveList = resolve;
        }),
    );

    // Mounted inactive, exactly as after a workspace switch to a non-Terminal
    // view: nothing is fetched and nothing is initialised.
    const { rerender, getAllByTestId, queryAllByTestId } = render(
      <Harness isActive={false} />,
    );
    expect(mockList).not.toHaveBeenCalled();

    // The user opens Terminal: isViewActive and enabled flip in one commit.
    rerender(<Harness isActive={true} />);

    await waitFor(() => expect(mockList).toHaveBeenCalledWith(WORKSPACE));

    // The GET is still in flight — nothing may be invented, and above all
    // nothing may be persisted.
    expect(queryAllByTestId("tab")).toHaveLength(0);
    expect(mockPut).not.toHaveBeenCalled();

    // The real tab list lands.
    await act(async () => {
      resolveList([makeMeta("lead-claude-1", 0), makeMeta("lead-codex-2", 1)]);
      await Promise.resolve();
    });

    await waitFor(() => expect(getAllByTestId("tab")).toHaveLength(2));
    expect(getAllByTestId("tab").map((el) => el.textContent)).toEqual([
      "lead-claude-1",
      "lead-codex-2",
    ]);

    // No stray lead-*-1 session was minted on the way in.
    expect(mockPut).not.toHaveBeenCalled();
  });

  it("still auto-creates a default tab for a genuinely empty workspace", async () => {
    mockList.mockResolvedValue([]);

    const { rerender, findAllByTestId } = render(<Harness isActive={false} />);
    rerender(<Harness isActive={true} />);

    const tabs = await findAllByTestId("tab");
    expect(tabs).toHaveLength(1);
    // Auto-created sessions are namespaced by workspace.
    expect(tabs[0].textContent).toBe(`${WORKSPACE}--lead-claude-1`);
    await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(1));
  });
});
