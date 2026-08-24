/**
 * BuiltinUpdateBanner — for a built-in workflow on the `pinned` track, surfaces
 * the packaged version this build ships as an available update (DEV-V5-33 D1),
 * or a packaging error, and offers to adopt it (auto track).
 */

import type { BuiltinVersionsInfo } from "@/api";

import styles from "./WorkflowsDashboard.module.css";

export interface BuiltinUpdateBannerProps {
  builtin?: BuiltinVersionsInfo | undefined;
  pending: boolean;
  onAdopt: () => void;
}

export function BuiltinUpdateBanner({
  builtin,
  pending,
  onAdopt,
}: BuiltinUpdateBannerProps): JSX.Element | null {
  if (!builtin) return null;

  if (builtin.packaged_error) {
    return (
      <div
        className={styles.banner}
        data-testid="builtin-update-banner"
        data-variant="error"
        role="status"
      >
        <span className={styles.bannerText}>
          Packaged built-in unavailable: {builtin.packaged_error}
        </span>
      </div>
    );
  }

  if (!builtin.update_available) return null;

  return (
    <div
      className={styles.banner}
      data-testid="builtin-update-banner"
      data-variant="update"
      role="status"
    >
      <span className={styles.bannerText}>
        Update available: packaged version{" "}
        <code>{builtin.packaged_version_id}</code>
        {builtin.packaged_source_digest
          ? ` (source ${builtin.packaged_source_digest.slice(0, 19)})`
          : ""}
      </span>
      <button
        type="button"
        className={styles.primaryButton}
        onClick={onAdopt}
        disabled={pending}
        data-testid="adopt-builtin-update"
      >
        Adopt update
      </button>
    </div>
  );
}
