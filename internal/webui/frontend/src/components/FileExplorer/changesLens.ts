import type { FileCheckout } from "@/api/workspace";
import type { WorkspaceStack } from "@/api/workspace";
import type { DiffFile } from "@/api/issues";
import { checkoutRefKey, type CheckoutRef } from "@/utils/fileExplorerRefs";

import { hasAvailableCheckoutStatus } from "./checkoutAvailability";

export type ChangeStatusKind = "modified" | "new" | "deleted" | "renamed";

export interface ChangeStatusChip {
  kind: ChangeStatusKind;
  label: "Modified" | "New" | "Deleted" | "Renamed";
}

export interface ChangeListItem {
  path: string;
  name: string;
  parentPath: string;
  status: ChangeStatusChip;
  additions?: number;
  deletions?: number;
}

export interface ChangeCheckoutGroup {
  id: string;
  ref: CheckoutRef;
  label: string;
  changeCount: number;
  loaded: boolean;
  items: ChangeListItem[];
  diffFrom?: string;
  diffTo?: string;
  diffTitle?: string;
  unavailable?: boolean;
}

export function checkoutRefFromCheckout(checkout: FileCheckout): CheckoutRef {
  if (checkout.kind === "agent") {
    return {
      scope: "agent",
      target: checkout.agent ?? "",
      repo: checkout.repo,
    };
  }
  return { scope: "repo", target: checkout.repo };
}

export function changeStatusFromPorcelain(xy: string): ChangeStatusChip {
  const code = xy.padEnd(2, " ").slice(0, 2);
  const [indexStatus, worktreeStatus] = code;
  if (code === "??" || indexStatus === "A" || worktreeStatus === "A") {
    return { kind: "new", label: "New" };
  }
  if (indexStatus === "R" || worktreeStatus === "R") {
    return { kind: "renamed", label: "Renamed" };
  }
  if (indexStatus === "D" || worktreeStatus === "D") {
    return { kind: "deleted", label: "Deleted" };
  }
  return { kind: "modified", label: "Modified" };
}

export function changeStatusFromDiffStatus(
  status: DiffFile["status"],
): ChangeStatusChip {
  if (status === "A") return { kind: "new", label: "New" };
  if (status === "D") return { kind: "deleted", label: "Deleted" };
  if (status === "R") return { kind: "renamed", label: "Renamed" };
  return { kind: "modified", label: "Modified" };
}

export function splitChangePath(path: string): {
  name: string;
  parentPath: string;
} {
  const normalized = path.replace(/^\/+|\/+$/g, "");
  const parts = normalized.split("/").filter(Boolean);
  const name = parts.pop() ?? normalized;
  return { name, parentPath: parts.join("/") };
}

function checkoutGroupLabel(checkout: FileCheckout): string {
  if (checkout.kind === "agent") {
    return `${checkout.agent ?? "agent"} · ${checkout.repo} · ${checkout.change_count}`;
  }
  return `${checkout.repo} · shared checkout · ${checkout.change_count}`;
}

function compareCheckouts(a: FileCheckout, b: FileCheckout): number {
  if (a.kind !== b.kind) return a.kind === "agent" ? -1 : 1;
  if (a.kind === "agent" && b.kind === "agent") {
    const agent = (a.agent ?? "").localeCompare(b.agent ?? "");
    if (agent !== 0) return agent;
  }
  return a.repo.localeCompare(b.repo);
}

export function buildChangeGroups(
  checkouts: FileCheckout[],
  gitStatusByRef: Record<string, Record<string, string>>,
): ChangeCheckoutGroup[] {
  return checkouts
    .filter(
      (checkout) =>
        hasAvailableCheckoutStatus(checkout) && checkout.change_count > 0,
    )
    .sort(compareCheckouts)
    .map((checkout) => {
      const ref = checkoutRefFromCheckout(checkout);
      const key = checkoutRefKey(ref);
      const status = gitStatusByRef[key];
      const items = Object.entries(status ?? {})
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([path, xy]) => {
          const { name, parentPath } = splitChangePath(path);
          return {
            path,
            name,
            parentPath,
            status: changeStatusFromPorcelain(xy),
          };
        });
      return {
        id: key,
        ref,
        label: checkoutGroupLabel(checkout),
        changeCount: checkout.change_count,
        loaded: status !== undefined,
        items,
      };
    });
}

export function buildBranchChangeGroups(
  checkouts: FileCheckout[],
  diffFilesByRef: Record<string, DiffFile[] | undefined>,
): ChangeCheckoutGroup[] {
  return checkouts
    .filter(
      (checkout) =>
        checkout.kind === "agent" && hasAvailableCheckoutStatus(checkout),
    )
    .sort(compareCheckouts)
    .flatMap((checkout) => {
      const ref = checkoutRefFromCheckout(checkout);
      const key = checkoutRefKey(ref);
      const diffFiles = diffFilesByRef[key];
      const loaded = diffFiles !== undefined;
      const count = diffFiles?.length ?? 0;
      if (loaded && count === 0) return [];
      const items = (diffFiles ?? [])
        .slice()
        .sort((a, b) => a.path.localeCompare(b.path))
        .map((file) => {
          const { name, parentPath } = splitChangePath(file.path);
          return {
            path: file.path,
            name,
            parentPath,
            status: changeStatusFromDiffStatus(file.status),
            additions: file.additions,
            deletions: file.deletions,
          };
        });
      return [
        {
          id: key,
          ref,
          label: `${checkout.agent ?? "agent"} · ${checkout.repo} · ${count}`,
          changeCount: count,
          loaded,
          items,
        },
      ];
    });
}

export function buildTaskChangeGroups(
  stacks: WorkspaceStack[],
  diffFilesByNode: Record<string, DiffFile[] | null | undefined>,
): ChangeCheckoutGroup[] {
  return stacks.flatMap((stack) =>
    stack.nodes.flatMap((node) => {
      if (!node.base_ref || !node.output_branch) return [];
      const id = `${stack.id}/${node.task_id}`;
      const diffFiles = diffFilesByNode[id];
      const loaded = diffFiles !== undefined;
      const unavailable = diffFiles === null;
      const count = unavailable ? 0 : (diffFiles?.length ?? 0);
      if (loaded && !unavailable && count === 0) return [];
      const items = (diffFiles ?? [])
        .slice()
        .sort((a, b) => a.path.localeCompare(b.path))
        .map((file) => {
          const { name, parentPath } = splitChangePath(file.path);
          return {
            path: file.path,
            name,
            parentPath,
            status: changeStatusFromDiffStatus(file.status),
            additions: file.additions,
            deletions: file.deletions,
          };
        });
      return [
        {
          id,
          ref: { scope: "repo", target: stack.repo },
          label: `${node.task_id} · ${stack.repo} · ${count}`,
          changeCount: count,
          loaded,
          items,
          diffFrom: node.base_ref,
          diffTo: node.output_branch,
          diffTitle: node.task_id,
          unavailable,
        },
      ];
    }),
  );
}
