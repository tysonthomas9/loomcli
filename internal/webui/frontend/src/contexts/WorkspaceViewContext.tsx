/**
 * WorkspaceViewContext — shell-to-view communication layer.
 *
 * Splits into two React contexts to prevent action-only consumers
 * from re-rendering when data changes:
 *   - WorkspaceViewDataContext: reactive data (issues, filters, connection state)
 *   - WorkspaceViewActionsContext: stable callbacks (handlers, navigation)
 */

import { createContext, useContext, type ReactNode } from "react";

import type { BlockedInfo } from "@/components/KanbanBoard";
import type { GroupByOption, FilterState } from "@/hooks/issues";
import type { ViewMode } from "@/components/ViewSwitcher";
import type { PanelState, ToastOptions } from "@/hooks/ui";
import type { Issue, IssueDetails, Status } from "@/types";
import type { LoomAgentStatus, LoomTaskInfo } from "@/types/agent";
import type { IssueContext } from "@/api/terminal";

// ---------------------------------------------------------------------------
// Data interface
// ---------------------------------------------------------------------------

export interface WorkspaceViewData {
  issues: Issue[];
  filteredIssues: Issue[];
  isLoading: boolean;
  error: string | null;
  connectionState: string;
  reconnectAttempts: number;
  pendingIds: Set<string> | undefined;
  blockedIssuesMap: Map<string, BlockedInfo> | undefined;
  filters: FilterState;
  groupBy: GroupByOption;
  debouncedSearch: string;
  activeView: ViewMode;
  selectedIssueId: string | null;
  workspaceId: string;
  isMultiRepo: boolean;
  agents: LoomAgentStatus[];
  agentTasks: Record<string, LoomTaskInfo>;
  issueDetails: Issue | IssueDetails | null;
  isLoadingDetails: boolean;
  detailError: string | null;
  previousView: ViewMode;
}

// ---------------------------------------------------------------------------
// Actions interface
// ---------------------------------------------------------------------------

export interface WorkspaceViewActions {
  refetch: () => void;
  updateIssueStatus: (issueId: string, newStatus: Status) => Promise<void>;
  fetchIssue: (issueId: string) => void;
  clearIssue: () => void;
  updateIssueDetails: (issue: Issue) => void;
  openPanel: (panel: NonNullable<PanelState>) => void;
  closePanel: () => void;
  handleIssueClick: (issue: Issue) => void;
  handlePanelClose: () => void;
  handleAgentClick: (agentName: string) => void;
  handleAgentPanelClose: () => void;
  handleAgentTaskClick: (taskId: string) => void;
  handleApprove: (issue: Issue) => Promise<void>;
  handleReject: (issue: Issue, comment: string) => Promise<void>;
  handleCopyLink: () => Promise<void>;
  navigateToView: (view: ViewMode) => void;
  showToast: (message: string, options?: ToastOptions) => string;
  setPendingIssueContext: (ctx: IssueContext | undefined) => void;
}

// ---------------------------------------------------------------------------
// Safe defaults (no-throw when used outside provider)
// ---------------------------------------------------------------------------

const noop = () => {};
const noopString = () => "";
const asyncNoop = async () => {};

export const NO_WORKSPACE_VIEW_DATA: WorkspaceViewData = {
  issues: [],
  filteredIssues: [],
  isLoading: false,
  error: null,
  connectionState: "disconnected",
  reconnectAttempts: 0,
  pendingIds: undefined,
  blockedIssuesMap: undefined,
  filters: {},
  groupBy: "none",
  debouncedSearch: "",
  activeView: "kanban",
  selectedIssueId: null,
  workspaceId: "",
  isMultiRepo: false,
  agents: [],
  agentTasks: {},
  issueDetails: null,
  isLoadingDetails: false,
  detailError: null,
  previousView: "kanban",
};

export const NO_WORKSPACE_VIEW_ACTIONS: WorkspaceViewActions = {
  refetch: noop,
  updateIssueStatus: asyncNoop,
  fetchIssue: noop,
  clearIssue: noop,
  updateIssueDetails: noop,
  openPanel: noop,
  closePanel: noop,
  handleIssueClick: noop,
  handlePanelClose: noop,
  handleAgentClick: noop,
  handleAgentPanelClose: noop,
  handleAgentTaskClick: noop,
  handleApprove: asyncNoop,
  handleReject: asyncNoop,
  handleCopyLink: asyncNoop,
  navigateToView: noop,
  showToast: noopString,
  setPendingIssueContext: noop,
};

// ---------------------------------------------------------------------------
// Contexts
// ---------------------------------------------------------------------------

const WorkspaceViewDataContext = createContext<WorkspaceViewData>(
  NO_WORKSPACE_VIEW_DATA,
);

const WorkspaceViewActionsContext = createContext<WorkspaceViewActions>(
  NO_WORKSPACE_VIEW_ACTIONS,
);

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

interface WorkspaceViewProviderProps {
  data: WorkspaceViewData;
  actions: WorkspaceViewActions;
  children: ReactNode;
}

export function WorkspaceViewProvider({
  data,
  actions,
  children,
}: WorkspaceViewProviderProps) {
  return (
    <WorkspaceViewDataContext.Provider value={data}>
      <WorkspaceViewActionsContext.Provider value={actions}>
        {children}
      </WorkspaceViewActionsContext.Provider>
    </WorkspaceViewDataContext.Provider>
  );
}

// ---------------------------------------------------------------------------
// Consumer hooks
// ---------------------------------------------------------------------------

export function useWorkspaceViewData(): WorkspaceViewData {
  return useContext(WorkspaceViewDataContext);
}

export function useWorkspaceViewActions(): WorkspaceViewActions {
  return useContext(WorkspaceViewActionsContext);
}
