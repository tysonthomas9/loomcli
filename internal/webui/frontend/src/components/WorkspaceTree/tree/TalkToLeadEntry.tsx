/**
 * TalkToLeadEntry renders a "Talk to Lead" action row in the workspace tree.
 * Shows an avatar icon, label, backend name chip, and status dot.
 */

import styles from "./EpicTaskTree.module.css";

export interface TalkToLeadEntryProps {
  workspaceName: string;
  /** Backend name from workspace config, defaults to 'claude'. */
  backend?: string | undefined;
  /** Click handler to open terminal for lead. */
  onTalkToLead?: ((workspaceName: string) => void) | undefined;
}

export function TalkToLeadEntry({
  workspaceName,
  backend,
  onTalkToLead,
}: TalkToLeadEntryProps): JSX.Element {
  const displayBackend = backend ?? "claude";

  return (
    <button
      type="button"
      className={styles.talkToLeadEntry}
      onClick={() => onTalkToLead?.(workspaceName)}
      title={`Talk to Lead (${displayBackend})`}
    >
      {/* Bot avatar icon */}
      <span className={styles.epicIcon}>
        <svg
          width="16"
          height="16"
          viewBox="0 0 16 16"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
        >
          <rect
            x="2"
            y="4"
            width="12"
            height="9"
            rx="2"
            stroke="currentColor"
            strokeWidth="1.3"
          />
          <circle cx="5.5" cy="8.5" r="1.25" fill="currentColor" />
          <circle cx="10.5" cy="8.5" r="1.25" fill="currentColor" />
          <line
            x1="8"
            y1="1.5"
            x2="8"
            y2="4"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinecap="round"
          />
          <circle cx="8" cy="1.5" r="1" fill="currentColor" />
        </svg>
      </span>
      <span className={styles.titleText}>Talk to Lead</span>
      <span className={styles.branchChip}>{displayBackend}</span>
      <span className={styles.statusDot} data-status="active" />
    </button>
  );
}
