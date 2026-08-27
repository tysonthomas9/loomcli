import type { FileTreeInlineEdit, FileTreeNodeInfo } from "./FileTree";
import type { RevisionViewState } from "./FileRevisionPane";
import type { FileBrowserMode } from "./treeRoots";
import type { FileIndexData } from "@/api/workspace";
import type { CheckoutRef } from "@/utils/fileExplorerRefs";
import type { ExplorerRef, SkillsExplorerRef } from "@/utils/explorerRefs";

export type ExplorerLens = "files" | "changes";
export type CompareMode = "branch" | "working";

export interface FileBrowserProps {
  mode?: FileBrowserMode | undefined;
  agentName?: string | undefined;
  isActive?: boolean | undefined;
}

export interface ContextMenuState {
  ref: ExplorerRef;
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
  ref: ExplorerRef;
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
  ref: ExplorerRef;
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

export interface CheckoutDiffViewState {
  kind: "checkout";
  ref: CheckoutRef;
  path: string;
  source?: "branch" | undefined;
  from?: string | undefined;
  to?: string | undefined;
  title: string;
  canOpenFile?: boolean | undefined;
}

export interface PatchDiffViewState {
  kind: "patch";
  path: string;
  title: string;
  patch: string;
}

export type DiffViewState = CheckoutDiffViewState | PatchDiffViewState;

export interface SkillGroupMenuState {
  ref: SkillsExplorerRef;
  x: number;
  y: number;
}

export interface DeleteSkillState {
  ref: SkillsExplorerRef;
  name: string;
}

export interface BranchDiffRequest {
  key: string;
  agent: string;
}

export interface ScopedFileIndex {
  ref: CheckoutRef;
  index: FileIndexData;
}

export type { RevisionViewState };
