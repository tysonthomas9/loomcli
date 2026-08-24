/**
 * WorkflowVersionsTable — the immutable version history for one workflow, newest
 * first, with per-row lifecycle actions (approve/unapprove, activate, rollback).
 * Columns surface active / approved / trust / provenance / bundle_verified so an
 * operator can reason about what is running and what is safe to activate.
 */

import type { WorkflowVersionItem } from "@/api";

import styles from "./WorkflowsDashboard.module.css";

export interface WorkflowVersionsTableProps {
  versions: WorkflowVersionItem[];
  pending: boolean;
  onApprove: (versionId: string) => void;
  onUnapprove: (versionId: string) => void;
  onActivate: (versionId: string) => void;
  onRollback: (versionId: string) => void;
}

function yesNo(value: boolean): string {
  return value ? "yes" : "no";
}

export function WorkflowVersionsTable({
  versions,
  pending,
  onApprove,
  onUnapprove,
  onActivate,
  onRollback,
}: WorkflowVersionsTableProps): JSX.Element {
  if (versions.length === 0) {
    return (
      <p className={styles.empty} data-testid="versions-empty">
        No versions yet.
      </p>
    );
  }

  // "Roll back to" is only meaningful for a version OLDER than the active one
  // (it activates that version AND pins the track, per DEV-V5-33 D4). For any
  // other non-active version, plain "Activate" is the accurate action, so we
  // don't show a misleading rollback control there.
  const activeVersionNumber = versions.find((v) => v.active)?.version.version;

  return (
    <table className={styles.table} data-testid="versions-table">
      <thead>
        <tr>
          <th>Version</th>
          <th>Active</th>
          <th>Approved</th>
          <th>Trust</th>
          <th>Provenance</th>
          <th>Bundle</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {versions.map((item) => {
          const id = item.version.version_id;
          const canRollBack =
            !item.active &&
            activeVersionNumber !== undefined &&
            item.version.version < activeVersionNumber;
          return (
            <tr key={id} data-testid={`version-row-${id}`} data-active={item.active}>
              <td>
                <code>{id}</code>
                {item.selected_by ? (
                  <span className={styles.subtle}> · by {item.selected_by}</span>
                ) : null}
              </td>
              <td data-testid={`version-active-${id}`}>{yesNo(item.active)}</td>
              <td data-testid={`version-approved-${id}`}>
                {yesNo(item.approved)}
              </td>
              <td>{item.effective_trust}</td>
              <td>{item.provenance ?? "—"}</td>
              <td data-testid={`version-bundle-${id}`}>
                {yesNo(item.bundle_verified)}
              </td>
              <td className={styles.rowActions}>
                {item.approved ? (
                  <button
                    type="button"
                    onClick={() => onUnapprove(id)}
                    disabled={pending}
                    data-testid={`unapprove-${id}`}
                  >
                    Unapprove
                  </button>
                ) : (
                  <button
                    type="button"
                    onClick={() => onApprove(id)}
                    disabled={pending}
                    data-testid={`approve-${id}`}
                  >
                    Approve
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => onActivate(id)}
                  disabled={pending || item.active}
                  data-testid={`activate-${id}`}
                >
                  Activate
                </button>
                {canRollBack ? (
                  <button
                    type="button"
                    onClick={() => onRollback(id)}
                    disabled={pending}
                    data-testid={`rollback-${id}`}
                  >
                    Roll back to
                  </button>
                ) : null}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
