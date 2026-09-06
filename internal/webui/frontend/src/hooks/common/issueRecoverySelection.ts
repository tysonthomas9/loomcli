import { createContext } from "react";
import { selectedHistoryIdentity } from "@/api/common/issueRecovery";
import type { IssueRecoverySelectionLease } from "./issueRecoveryAttempt";

type Owner = { workspace: string; issueId: string };
type Capture = { generation: number; controller: AbortController };

/** Tracks committed history owners. It never selects a panel during render or
 * chooses one of several conflicting scopes to manufacture recovery coverage. */
export class IssueRecoverySelectionRegistry {
  private owners = new Map<symbol, Owner>();
  private generation = 0;
  private active: Capture | undefined;

  register(workspace: string, issueId: string): () => void {
    const token = Symbol("history owner");
    this.owners.set(token, { workspace, issueId });
    this.invalidate();
    return () => {
      if (!this.owners.delete(token)) return;
      this.invalidate();
    };
  }

  capture(workspace: string): IssueRecoverySelectionLease {
    const generation = this.invalidate();
    if (generation !== this.generation)
      throw new Error("Recovery selection superseded during capture");
    const issues = new Set<string>();
    for (const owner of this.owners.values()) {
      if (owner.workspace === workspace)
        issues.add(selectedHistoryIdentity(owner.issueId));
    }
    if (issues.size > 1)
      throw new Error("Recovery requires more than one selected history");
    const issueId = issues.values().next().value;
    const entry: Capture = { generation, controller: new AbortController() };
    this.active = entry;
    const current = () =>
      this.active === entry &&
      this.generation === entry.generation &&
      !entry.controller.signal.aborted;
    return Object.freeze({
      issueId,
      signal: entry.controller.signal,
      isCurrent: current,
      release: () => {
        if (this.active !== entry) return;
        this.active = undefined;
        entry.controller.abort();
      },
    });
  }

  private invalidate(): number {
    const generation = ++this.generation;
    const previous = this.active;
    this.active = undefined;
    // Mutations precede notification so reentrant observers see the new set.
    previous?.controller.abort();
    return generation;
  }
}

export const IssueRecoverySelectionContext =
  createContext<IssueRecoverySelectionRegistry | null>(null);
