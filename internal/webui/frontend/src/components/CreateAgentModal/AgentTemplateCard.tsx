import type { CSSProperties } from "react";

import styles from "./CreateAgentModal.module.css";

export interface AgentTemplateCardProps {
  title: string;
  description: string;
  glyph: string;
  accentColor: string;
  selected: boolean;
  disabled?: boolean;
  ariaLabel: string;
  testId?: string;
  onSelect: () => void;
}

export function AgentTemplateCard({
  title,
  description,
  glyph,
  accentColor,
  selected,
  disabled = false,
  ariaLabel,
  testId,
  onSelect,
}: AgentTemplateCardProps): JSX.Element {
  return (
    <button
      type="button"
      className={styles.templateCard}
      data-active={selected || undefined}
      aria-pressed={selected}
      aria-label={ariaLabel}
      disabled={disabled}
      data-testid={testId}
      onClick={onSelect}
      style={{ "--template-accent": accentColor } as CSSProperties}
    >
      <span
        className={styles.templateGlyph}
        style={{ backgroundColor: accentColor }}
        aria-hidden="true"
      >
        {glyph}
      </span>
      <span className={styles.templateBody}>
        <span className={styles.templateTitleRow}>
          <span className={styles.templateTitle}>{title}</span>
          {selected ? (
            <span className={styles.templateSelectedMark} aria-hidden="true">
              Selected
            </span>
          ) : null}
        </span>
        <span className={styles.templateDescription}>{description}</span>
      </span>
    </button>
  );
}
