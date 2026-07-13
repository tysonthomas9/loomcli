import {
  useEffect,
  useState,
  type KeyboardEvent,
  type MouseEvent,
} from "react";

import { useScopedFileTree } from "@/hooks";
import type { FileBrowserTab } from "@/stores";
import {
  checkoutLabel,
  checkoutRefKey,
  sameCheckoutRef,
  type CheckoutRef,
} from "@/utils/fileExplorerRefs";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import {
  FileTree,
  type FileTreeInlineEdit,
  type FileTreeNodeInfo,
} from "./FileTree";
import type { ChangeCheckoutGroup } from "./changesLens";
import { unavailableCheckoutSummary } from "./checkoutAvailability";
import { dirname } from "./fileExplorerLocalUtils";
import type { HistoryOpenDiffRequest } from "./FileHistoryPanel";
import styles from "./FileExplorer.module.css";
import type {
  CompareMode,
  ExplorerLens,
  ScopedInlineEdit,
  TreeRefreshRequest,
  TreeRevealRequest,
} from "./workspaceFileBrowserTypes";
import type { FileTreeRoot, FileTreeSection } from "./treeRoots";

const CHECKOUT_UNAVAILABLE_MESSAGE = "This checkout is not checked out";
const GIT_STATUS_UNAVAILABLE_MESSAGE =
  "Git status unavailable - decorations and changes are hidden for this checkout";

function LensToggle({
  lens,
  changeCount,
  onChange,
}: {
  lens: ExplorerLens;
  changeCount: number;
  onChange: (lens: ExplorerLens) => void;
}) {
  const tabs: Array<{ id: ExplorerLens; label: string }> = [
    { id: "files", label: "Files" },
    { id: "changes", label: "Changes" },
  ];

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const currentIndex = tabs.findIndex((tab) => tab.id === lens);
    let nextIndex = -1;
    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      nextIndex = (currentIndex + 1) % tabs.length;
    } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      nextIndex = (currentIndex - 1 + tabs.length) % tabs.length;
    }
    if (nextIndex >= 0) {
      event.preventDefault();
      onChange(tabs[nextIndex]!.id);
    }
  };

  return (
    <div
      className={styles.lensToggle}
      role="tablist"
      aria-label="File explorer lens"
      onKeyDown={handleKeyDown}
    >
      {tabs.map((tab) => {
        const active = lens === tab.id;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            className={styles.lensTab}
            data-active={active || undefined}
            aria-selected={active}
            aria-label={
              tab.id === "changes" ? `Changes ${changeCount}` : tab.label
            }
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(tab.id)}
          >
            <span>{tab.label}</span>
            {tab.id === "changes" && (
              <span className={styles.lensBadge}>{changeCount}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

function CompareToggle({
  compareMode,
  branchChangeCount,
  taskChangeCount,
  workingChangeCount,
  branchBaseName,
  showTaskCompareMode,
  onChange,
}: {
  compareMode: CompareMode;
  branchChangeCount: number;
  taskChangeCount: number;
  workingChangeCount: number;
  branchBaseName?: string | undefined;
  showTaskCompareMode: boolean;
  onChange: (compareMode: CompareMode) => void;
}) {
  const tabs: Array<{
    id: CompareMode;
    label: string;
    count: number;
    title: string;
  }> = [
    {
      id: "branch",
      label: `Branch vs ${branchBaseName ?? "base"}`,
      count: branchChangeCount,
      title: `Committed changes on each agent branch, diffed against the merge-base with ${branchBaseName ?? "the repo default branch"}.`,
    },
    ...(showTaskCompareMode
      ? [
          {
            id: "tasks" as const,
            label: "By task",
            count: taskChangeCount,
            title:
              "Each task's committed increment: its stack base branch diffed against its output branch.",
          },
        ]
      : []),
    {
      id: "working",
      label: "Working tree",
      count: workingChangeCount,
      title:
        "Uncommitted changes in each checkout — like git status. Committed work does not appear here.",
    },
  ];
  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const currentIndex = tabs.findIndex((tab) => tab.id === compareMode);
    let nextIndex = -1;
    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      nextIndex = (currentIndex + 1) % tabs.length;
    } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      nextIndex = (currentIndex - 1 + tabs.length) % tabs.length;
    }
    if (nextIndex >= 0) {
      event.preventDefault();
      onChange(tabs[nextIndex]!.id);
    }
  };
  return (
    <div
      className={styles.compareToggle}
      role="tablist"
      aria-label="Compare mode"
      onKeyDown={handleKeyDown}
    >
      {tabs.map((tab) => {
        const active = compareMode === tab.id;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            className={styles.compareTab}
            data-active={active || undefined}
            aria-selected={active}
            tabIndex={active ? 0 : -1}
            title={tab.title}
            onClick={() => onChange(tab.id)}
          >
            {tab.label} · {tab.count}
          </button>
        );
      })}
    </div>
  );
}

function ChangesList({
  groups,
  unavailableLabels,
  compareMode,
  showSharedCheckoutNotice,
  onOpenDiff,
}: {
  groups: ChangeCheckoutGroup[];
  unavailableLabels: string[];
  compareMode: CompareMode;
  showSharedCheckoutNotice: boolean;
  onOpenDiff: (request: HistoryOpenDiffRequest) => void;
}) {
  const unavailableText = unavailableCheckoutSummary(unavailableLabels);
  const emptyText =
    compareMode === "branch"
      ? "No committed changes vs base across this workspace."
      : compareMode === "tasks"
        ? "No task increments found in this workspace's stacks."
        : "No uncommitted changes across this workspace.";
  if (
    groups.length === 0 &&
    unavailableLabels.length === 0 &&
    !showSharedCheckoutNotice
  ) {
    return <div className={styles.changesEmpty}>{emptyText}</div>;
  }

  const openDiffRequest = (
    group: ChangeCheckoutGroup,
    item: ChangeCheckoutGroup["items"][number],
  ): HistoryOpenDiffRequest => {
    if (compareMode === "branch") {
      return {
        ref: group.ref,
        path: item.path,
        source: "branch",
        title: checkoutLabel(group.ref),
        canOpenFile: item.status.kind !== "deleted",
      };
    }
    if (compareMode === "tasks") {
      return {
        ref: group.ref,
        path: item.path,
        from: group.diffFrom,
        to: group.diffTo,
        title: group.diffTitle,
        canOpenFile: false,
      };
    }
    return {
      ref: group.ref,
      path: item.path,
      from: "HEAD",
      title: checkoutLabel(group.ref),
      canOpenFile: item.status.kind !== "deleted",
    };
  };

  const renderStats = (
    item: ChangeCheckoutGroup["items"][number],
  ): JSX.Element | null => {
    const hasAdditions = item.additions !== undefined && item.additions !== 0;
    const hasDeletions = item.deletions !== undefined && item.deletions !== 0;
    if (!hasAdditions && !hasDeletions) return null;
    return (
      <span className={styles.changeStats} aria-label="Line changes">
        {hasAdditions && (
          <span className={styles.changeStatAdd}>+{item.additions}</span>
        )}
        {hasDeletions && (
          <span className={styles.changeStatDelete}>−{item.deletions}</span>
        )}
      </span>
    );
  };

  return (
    <div className={styles.changesList} aria-label="Workspace changes">
      {unavailableText && (
        <div className={styles.changesNotice} role="status">
          {unavailableText}
        </div>
      )}
      {showSharedCheckoutNotice && (
        <div className={styles.changesNotice} role="status">
          Shared checkout changes appear under Working tree.
        </div>
      )}
      {groups.length === 0 && (
        <div className={styles.changesEmpty}>{emptyText}</div>
      )}
      {groups.map((group) => (
        <section key={group.id} className={styles.changesGroup}>
          <h2 className={styles.changesGroupHeader}>{group.label}</h2>
          {group.unavailable ? (
            <div className={styles.changesLoading}>
              Task increment unavailable
            </div>
          ) : !group.loaded ? (
            <div className={styles.changesLoading}>Loading changes...</div>
          ) : group.items.length === 0 ? (
            <div className={styles.changesLoading}>No changed files found</div>
          ) : (
            group.items.map((item) => (
              <button
                type="button"
                key={item.path}
                className={styles.changeRow}
                aria-label={`Open diff for ${item.path} (${item.status.label})`}
                onClick={() => onOpenDiff(openDiffRequest(group, item))}
              >
                <span className={styles.changePath}>
                  <span className={styles.changeName}>{item.name}</span>
                  {item.parentPath && (
                    <span className={styles.changeParent}>
                      {item.parentPath}
                    </span>
                  )}
                </span>
                <span className={styles.changeMeta}>
                  {renderStats(item)}
                  <span
                    className={styles.changeStatusChip}
                    data-status={item.status.kind}
                  >
                    {item.status.label}
                  </span>
                </span>
              </button>
            ))
          )}
        </section>
      ))}
    </div>
  );
}

function hasRepoScopeChanges(sections: FileTreeSection[]): boolean {
  return sections.some((section) =>
    section.roots.some((root) => {
      if (root.kind === "checkout") {
        return root.ref.scope === "repo" && root.changeCount > 0;
      }
      return root.children.some(
        (child) => child.ref.scope === "repo" && child.changeCount > 0,
      );
    }),
  );
}

function ChangeBadge({ count }: { count: number }): JSX.Element | null {
  if (count <= 0) return null;
  return <span className={styles.checkoutBadge}>{count}</span>;
}

function WarningIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 16 16"
      className={styles.checkoutStatusWarningIcon}
      aria-hidden="true"
    >
      <path
        d="M8 2.4 14 13H2L8 2.4z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <path
        d="M8 6v3.2M8 11.5h.01"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}

function CheckoutRepairButton({
  state,
  label,
  busy,
  onRepair,
}: {
  state: "unavailable" | "missing";
  label: string;
  busy: boolean;
  onRepair: () => void;
}): JSX.Element {
  const text = busy
    ? "Repairing..."
    : state === "unavailable"
      ? "Unavailable"
      : "Not checked out";
  const detail =
    state === "unavailable"
      ? GIT_STATUS_UNAVAILABLE_MESSAGE
      : CHECKOUT_UNAVAILABLE_MESSAGE;
  return (
    <button
      type="button"
      className={styles.checkoutRepairButton}
      data-state={state}
      aria-label={`Repair checkout for ${label}: ${detail}`}
      title={`Repair checkout: ${detail}`}
      disabled={busy}
      onClick={onRepair}
    >
      {state === "unavailable" && <WarningIcon />}
      <span>{text}</span>
    </button>
  );
}

function AgentAvatar({ name }: { name: string }): JSX.Element {
  const bg = getAvatarColor(name);
  return (
    <span
      className={styles.agentAvatar}
      style={{
        background: bg,
        color: shouldUseWhiteText(bg) ? "#fff" : "#1a1a1a",
      }}
      aria-hidden="true"
    >
      {getCompactAvatarInitials(name)}
    </span>
  );
}

function RootIcon({ icon }: { icon: "agent" | "repo" | "workspace" }) {
  if (icon === "agent") return null;
  return (
    <span className={styles.rootIcon} aria-hidden="true">
      {icon === "workspace" ? (
        <svg viewBox="0 0 16 16">
          <path
            d="M2.5 4.5h11v8h-11zM4 3h8"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinejoin="round"
          />
        </svg>
      ) : (
        <svg viewBox="0 0 16 16">
          <path
            d="M2 3h4l2 2h6v8H2V3z"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinejoin="round"
          />
        </svg>
      )}
    </span>
  );
}

function CheckoutUnavailableState({
  depthOffset,
}: {
  depthOffset: number;
}): JSX.Element {
  return (
    <div
      className={styles.checkoutTreeState}
      style={{ paddingLeft: 8 + depthOffset * 16 }}
    >
      {CHECKOUT_UNAVAILABLE_MESSAGE}
    </div>
  );
}

function RootRow({
  root,
  expanded,
  depth = 0,
  repairing,
  canWrite,
  onToggle,
  onRepairCheckout,
  onCheckoutContextMenu,
}: {
  root: FileTreeRoot;
  expanded: boolean;
  depth?: number | undefined;
  repairing: boolean;
  canWrite: boolean;
  onToggle: () => void;
  onRepairCheckout: (ref: CheckoutRef, label: string) => void;
  onCheckoutContextMenu: (
    ref: CheckoutRef,
    label: string,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
}) {
  const isAgent = root.kind === "agent";
  const label = root.label;
  const secondary = root.secondary;
  const exists = root.exists;
  const icon = isAgent ? "agent" : root.icon;
  const rowTitle = exists ? undefined : CHECKOUT_UNAVAILABLE_MESSAGE;
  const repairState = root.gitStatusUnavailable
    ? "unavailable"
    : !root.exists
      ? "missing"
      : null;
  const repairRef =
    root.kind === "checkout" ? root.ref : (root.flattenedRef ?? null);
  const canRepair =
    canWrite &&
    repairState != null &&
    repairRef != null &&
    repairRef.scope !== "workspace";
  const repairLabel = secondary ? `${label} ${secondary}` : label;
  return (
    <div
      className={styles.rootRow}
      data-dimmed={root.kind === "checkout" && root.dimmed ? true : undefined}
      data-disabled={!exists || undefined}
      title={rowTitle}
      onContextMenu={(event) => {
        if (!canRepair || !repairRef) return;
        event.preventDefault();
        onCheckoutContextMenu(repairRef, repairLabel, event);
      }}
    >
      <button
        type="button"
        className={styles.rootRowToggle}
        style={{ paddingLeft: 8 + depth * 16 }}
        disabled={!exists}
        onClick={onToggle}
      >
        <span
          className={`${styles.chevron} ${expanded ? styles.chevronExpanded : ""}`}
          aria-hidden="true"
        >
          <svg viewBox="0 0 16 16">
            <path
              d="M6 4l4 4-4 4"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
            />
          </svg>
        </span>
        {isAgent ? (
          <AgentAvatar name={root.agentName} />
        ) : (
          <RootIcon icon={icon} />
        )}
        <span className={styles.rootLabel}>{label}</span>
        {secondary && (
          <span className={styles.rootSecondary}>· {secondary}</span>
        )}
        {!repairState && <ChangeBadge count={root.changeCount} />}
      </button>
      {repairState && canRepair && repairRef && (
        <CheckoutRepairButton
          state={repairState}
          label={repairLabel}
          busy={repairing}
          onRepair={() => onRepairCheckout(repairRef, repairLabel)}
        />
      )}
    </div>
  );
}

function sameRefInlineEdit(
  inlineEdit: ScopedInlineEdit | null,
  ref: CheckoutRef,
): FileTreeInlineEdit | null {
  return inlineEdit && sameCheckoutRef(inlineEdit.ref, ref)
    ? inlineEdit.edit
    : null;
}

function CheckoutTreeBlock({
  refInfo,
  depthOffset,
  selectedTab,
  inlineEdit,
  gitStatus,
  revealRequest,
  refreshRequest,
  onOpenFile,
  onContextMenu,
  onRequestRename,
  onRequestDelete,
  onInlineEditChange,
  onInlineEditCommit,
  onInlineEditCancel,
}: {
  refInfo: CheckoutRef;
  depthOffset: number;
  selectedTab: FileBrowserTab | null;
  inlineEdit: ScopedInlineEdit | null;
  gitStatus: Record<string, string>;
  revealRequest?: TreeRevealRequest | undefined;
  refreshRequest?: TreeRefreshRequest | undefined;
  onOpenFile: (ref: CheckoutRef, path: string) => void;
  onContextMenu: (
    ref: CheckoutRef,
    node: FileTreeNodeInfo,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
  onRequestRename?:
    | ((ref: CheckoutRef, node: FileTreeNodeInfo) => void)
    | undefined;
  onRequestDelete?:
    | ((ref: CheckoutRef, node: FileTreeNodeInfo) => void)
    | undefined;
  onInlineEditChange: (value: string) => void;
  onInlineEditCommit: () => void;
  onInlineEditCancel: () => void;
}) {
  const {
    expanded,
    treeData,
    isLoading,
    error,
    debouncedFilterText,
    toggle,
    loadDir,
    revealPath,
  } = useScopedFileTree(refInfo);
  const [scrollTarget, setScrollTarget] = useState<string | null>(null);
  const selectedPath =
    selectedTab && sameCheckoutRef(selectedTab.ref, refInfo)
      ? selectedTab.path
      : null;

  useEffect(() => {
    if (!revealRequest) return;
    void revealPath(revealRequest.path).then(() =>
      setScrollTarget(revealRequest.path),
    );
  }, [revealPath, revealRequest]);

  useEffect(() => {
    if (!refreshRequest) return;
    const parents = new Set(refreshRequest.paths.map(dirname));
    void Promise.all([...parents].map((parent) => loadDir(parent)));
  }, [loadDir, refreshRequest]);

  if (isLoading) {
    return (
      <div
        className={styles.checkoutTreeState}
        style={{ paddingLeft: 8 + depthOffset * 16 }}
      >
        Loading...
      </div>
    );
  }
  if (error) {
    return <CheckoutUnavailableState depthOffset={depthOffset} />;
  }

  return (
    <FileTree
      treeData={treeData}
      expanded={expanded}
      selectedPath={selectedPath}
      filterText={debouncedFilterText}
      onToggle={toggle}
      onSelectFile={(path) => {
        if (path) onOpenFile(refInfo, path);
      }}
      onContextMenuNode={(node, event) => onContextMenu(refInfo, node, event)}
      {...(onRequestRename
        ? {
            onRequestRename: (node: FileTreeNodeInfo) =>
              onRequestRename(refInfo, node),
          }
        : {})}
      {...(onRequestDelete
        ? {
            onRequestDelete: (node: FileTreeNodeInfo) =>
              onRequestDelete(refInfo, node),
          }
        : {})}
      inlineEdit={sameRefInlineEdit(inlineEdit, refInfo)}
      onInlineEditChange={onInlineEditChange}
      onInlineEditCommit={onInlineEditCommit}
      onInlineEditCancel={onInlineEditCancel}
      gitStatus={gitStatus}
      scrollToPath={scrollTarget}
      depthOffset={depthOffset}
      idPrefix={`ft-${checkoutRefKey(refInfo).replace(/[^a-zA-Z0-9_-]/g, "-")}`}
    />
  );
}

export function FileExplorerTreePanel({
  lens,
  changeCount,
  compareMode,
  branchChangeCount,
  taskChangeCount,
  workingChangeCount,
  branchBaseName,
  showTaskCompareMode,
  checkoutError,
  repairError,
  sections,
  changeGroups,
  unavailableCheckoutLabels,
  expandedRoots,
  repairingCheckoutKey,
  canWrite,
  selectedTab,
  inlineEdit,
  gitStatusByRef,
  treeRevealRequests,
  treeRefreshRequests,
  hideAgentSectionHeading,
  onLensChange,
  onCompareModeChange,
  onQuickOpen,
  onOpenDiff,
  onToggleRoot,
  onRepairCheckout,
  onCheckoutContextMenu,
  onOpenFile,
  onContextMenu,
  onRequestRename,
  onRequestDelete,
  onInlineEditChange,
  onInlineEditCommit,
  onInlineEditCancel,
}: {
  lens: ExplorerLens;
  changeCount: number;
  compareMode: CompareMode;
  branchChangeCount: number;
  taskChangeCount: number;
  workingChangeCount: number;
  branchBaseName?: string | undefined;
  showTaskCompareMode: boolean;
  checkoutError: string | null;
  repairError: string | null;
  sections: FileTreeSection[];
  changeGroups: ChangeCheckoutGroup[];
  unavailableCheckoutLabels: string[];
  expandedRoots: Set<string>;
  repairingCheckoutKey: string | null;
  canWrite: boolean;
  selectedTab: FileBrowserTab | null;
  inlineEdit: ScopedInlineEdit | null;
  gitStatusByRef: Record<string, Record<string, string>>;
  treeRevealRequests: Record<string, TreeRevealRequest>;
  treeRefreshRequests: Record<string, TreeRefreshRequest>;
  hideAgentSectionHeading: boolean;
  onLensChange: (lens: ExplorerLens) => void;
  onCompareModeChange: (compareMode: CompareMode) => void;
  onQuickOpen: () => void;
  onOpenDiff: (request: HistoryOpenDiffRequest) => void;
  onToggleRoot: (key: string) => void;
  onRepairCheckout: (ref: CheckoutRef, label: string) => void;
  onCheckoutContextMenu: (
    ref: CheckoutRef,
    label: string,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
  onOpenFile: (ref: CheckoutRef, path: string) => void;
  onContextMenu: (
    ref: CheckoutRef,
    node: FileTreeNodeInfo,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
  onRequestRename: (ref: CheckoutRef, node: FileTreeNodeInfo) => void;
  onRequestDelete: (ref: CheckoutRef, node: FileTreeNodeInfo) => void;
  onInlineEditChange: (value: string) => void;
  onInlineEditCommit: () => void;
  onInlineEditCancel: () => void;
}) {
  const showSharedCheckoutNotice =
    compareMode === "branch" && hasRepoScopeChanges(sections);
  const renderCheckoutRoot = (
    root: Extract<FileTreeRoot, { kind: "checkout" }>,
    depth: number,
  ) => {
    const key = checkoutRefKey(root.ref);
    const expanded = expandedRoots.has(key);
    return (
      <div key={root.id}>
        <RootRow
          root={root}
          depth={depth}
          expanded={expanded}
          repairing={repairingCheckoutKey === key}
          canWrite={canWrite}
          onToggle={() => onToggleRoot(key)}
          onRepairCheckout={onRepairCheckout}
          onCheckoutContextMenu={onCheckoutContextMenu}
        />
        {expanded &&
          (root.exists ? (
            <CheckoutTreeBlock
              refInfo={root.ref}
              depthOffset={depth + 1}
              selectedTab={selectedTab}
              inlineEdit={inlineEdit}
              gitStatus={gitStatusByRef[key] ?? {}}
              revealRequest={treeRevealRequests[key]}
              refreshRequest={treeRefreshRequests[key]}
              onOpenFile={onOpenFile}
              onContextMenu={onContextMenu}
              onRequestRename={canWrite ? onRequestRename : undefined}
              onRequestDelete={canWrite ? onRequestDelete : undefined}
              onInlineEditChange={onInlineEditChange}
              onInlineEditCommit={onInlineEditCommit}
              onInlineEditCancel={onInlineEditCancel}
            />
          ) : (
            <CheckoutUnavailableState depthOffset={depth + 1} />
          ))}
      </div>
    );
  };

  const renderRoot = (root: FileTreeRoot) => {
    if (root.kind === "checkout") return renderCheckoutRoot(root, 0);
    if (root.flattenedRef) {
      const key = checkoutRefKey(root.flattenedRef);
      const expanded = expandedRoots.has(key);
      return (
        <div key={root.id}>
          <RootRow
            root={root}
            expanded={expanded}
            repairing={repairingCheckoutKey === key}
            canWrite={canWrite}
            onToggle={() => onToggleRoot(key)}
            onRepairCheckout={onRepairCheckout}
            onCheckoutContextMenu={onCheckoutContextMenu}
          />
          {expanded &&
            (root.exists ? (
              <CheckoutTreeBlock
                refInfo={root.flattenedRef}
                depthOffset={1}
                selectedTab={selectedTab}
                inlineEdit={inlineEdit}
                gitStatus={gitStatusByRef[key] ?? {}}
                revealRequest={treeRevealRequests[key]}
                refreshRequest={treeRefreshRequests[key]}
                onOpenFile={onOpenFile}
                onContextMenu={onContextMenu}
                onRequestRename={canWrite ? onRequestRename : undefined}
                onRequestDelete={canWrite ? onRequestDelete : undefined}
                onInlineEditChange={onInlineEditChange}
                onInlineEditCommit={onInlineEditCommit}
                onInlineEditCancel={onInlineEditCancel}
              />
            ) : (
              <CheckoutUnavailableState depthOffset={1} />
            ))}
        </div>
      );
    }
    const expanded = expandedRoots.has(root.id);
    return (
      <div key={root.id}>
        <RootRow
          root={root}
          expanded={expanded}
          repairing={repairingCheckoutKey === root.id}
          canWrite={canWrite}
          onToggle={() => onToggleRoot(root.id)}
          onRepairCheckout={onRepairCheckout}
          onCheckoutContextMenu={onCheckoutContextMenu}
        />
        {expanded && root.children.map((child) => renderCheckoutRoot(child, 1))}
      </div>
    );
  };

  const renderWorkspaceSection = (
    section: FileTreeSection,
    root: Extract<FileTreeRoot, { kind: "checkout" }>,
  ) => {
    const key = checkoutRefKey(root.ref);
    const expanded = expandedRoots.has(key);
    return (
      <section
        key={section.id}
        className={styles.rootSection}
        data-dimmed={section.dimmed || undefined}
      >
        <h2 className={styles.rootSectionHeading}>
          <button
            type="button"
            className={styles.workspaceSectionToggle}
            aria-expanded={expanded}
            onClick={() => onToggleRoot(key)}
          >
            <span
              className={`${styles.chevron} ${expanded ? styles.chevronExpanded : ""}`}
              aria-hidden="true"
            >
              <svg viewBox="0 0 16 16">
                <path
                  d="M6 4l4 4-4 4"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                />
              </svg>
            </span>
            <span>{section.title}</span>
            {root.changeCount > 0 && (
              <span className={styles.checkoutBadge}>{root.changeCount}</span>
            )}
          </button>
        </h2>
        {expanded &&
          (root.exists ? (
            <CheckoutTreeBlock
              refInfo={root.ref}
              depthOffset={0}
              selectedTab={selectedTab}
              inlineEdit={inlineEdit}
              gitStatus={gitStatusByRef[key] ?? {}}
              revealRequest={treeRevealRequests[key]}
              refreshRequest={treeRefreshRequests[key]}
              onOpenFile={onOpenFile}
              onContextMenu={onContextMenu}
              onRequestRename={canWrite ? onRequestRename : undefined}
              onRequestDelete={canWrite ? onRequestDelete : undefined}
              onInlineEditChange={onInlineEditChange}
              onInlineEditCommit={onInlineEditCommit}
              onInlineEditCancel={onInlineEditCancel}
            />
          ) : (
            <CheckoutUnavailableState depthOffset={0} />
          ))}
      </section>
    );
  };

  return (
    <>
      <div className={styles.toolbar}>
        <LensToggle
          lens={lens}
          changeCount={changeCount}
          onChange={onLensChange}
        />
        {lens === "changes" && (
          <CompareToggle
            compareMode={compareMode}
            branchChangeCount={branchChangeCount}
            taskChangeCount={taskChangeCount}
            workingChangeCount={workingChangeCount}
            branchBaseName={branchBaseName}
            showTaskCompareMode={showTaskCompareMode}
            onChange={onCompareModeChange}
          />
        )}
        <button
          type="button"
          className={styles.quickOpenButton}
          onClick={onQuickOpen}
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <circle
              cx="7"
              cy="7"
              r="4"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.4"
            />
            <path
              d="M10.5 10.5L14 14"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.4"
              strokeLinecap="round"
            />
          </svg>
          <span>Go to file...</span>
          <kbd>Cmd+P</kbd>
        </button>
      </div>
      <div className={styles.treeScroll}>
        {checkoutError && (
          <div className={styles.checkoutError}>{checkoutError}</div>
        )}
        {repairError && (
          <div className={styles.checkoutError}>{repairError}</div>
        )}
        {lens === "changes" ? (
          <ChangesList
            groups={changeGroups}
            unavailableLabels={unavailableCheckoutLabels}
            compareMode={compareMode}
            showSharedCheckoutNotice={showSharedCheckoutNotice}
            onOpenDiff={onOpenDiff}
          />
        ) : (
          sections.map((section) => {
            const workspaceRoot =
              section.id === "workspace"
                ? section.roots.find(
                    (
                      root,
                    ): root is Extract<FileTreeRoot, { kind: "checkout" }> =>
                      root.kind === "checkout" &&
                      root.ref.scope === "workspace",
                  )
                : undefined;
            if (workspaceRoot) {
              return renderWorkspaceSection(section, workspaceRoot);
            }
            if (hideAgentSectionHeading && section.id === "agents") {
              return (
                <div key={section.id} className={styles.rootSection}>
                  {section.roots.length === 0 ? (
                    <div className={styles.rootSectionEmpty}>None</div>
                  ) : (
                    section.roots.map(renderRoot)
                  )}
                </div>
              );
            }
            return (
              <section
                key={section.id}
                className={styles.rootSection}
                data-dimmed={section.dimmed || undefined}
              >
                <h2 className={styles.rootSectionHeading}>{section.title}</h2>
                {section.roots.length === 0 ? (
                  <div className={styles.rootSectionEmpty}>None</div>
                ) : (
                  section.roots.map(renderRoot)
                )}
              </section>
            );
          })
        )}
      </div>
    </>
  );
}
