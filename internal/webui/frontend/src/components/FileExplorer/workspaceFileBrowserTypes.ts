import type { FileTreeInlineEdit, FileTreeNodeInfo } from "./FileTree";
import type { RevisionViewState } from "./FileRevisionPane";
import type { FileBrowserMode } from "./treeRoots";
import type { CheckoutRef } from "@/utils/fileExplorerRefs";

export type ExplorerLens = "files" | "changes";
export type CompareMode = "branch" | "tasks" | "working";

export interface FileBrowserProps {
  mode?: FileBrowserMode | undefined;
  agentName?: string | undefined;
  isActive?: boolean | undefined;
}

export interface ContextMenuState {
  ref: CheckoutRef;
  node: FileTreeNodeInfo;
  x: number;
  y: number;
  duplicateEligible: boolean;
}

export interface CheckoutRepairMenuState {
  ref: CheckoutRef;
  label: string;
  x: number;
  y: number;
}

export interface DeleteConfirmState {
  ref: CheckoutRef;
  node: FileTreeNodeInfo;
}

export interface MoveDialogState {
  ref: CheckoutRef;
  node: FileTreeNodeInfo;
}

export interface RepairConfirmState {
  ref: CheckoutRef;
  label: string;
}

export interface ScopedInlineEdit {
  ref: CheckoutRef;
  edit: FileTreeInlineEdit;
}

export interface TreeRevealRequest {
  path: string;
  token: number;
}

export interface TreeRefreshRequest {
  paths: string[];
  token: number;
}

export interface LineTarget {
  line: number;
  token: number;
}

export interface DiffViewState {
  ref: CheckoutRef;
  path: string;
  source?: "branch" | undefined;
  from?: string | undefined;
  to?: string | undefined;
  title: string;
  patch?: string | undefined;
  canOpenFile?: boolean | undefined;
}

export type { RevisionViewState };
