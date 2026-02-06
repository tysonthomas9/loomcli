import styles from './TalkToLeadButton.module.css';

export interface TalkToLeadButtonProps {
  onClick?: () => void;
  isActive?: boolean;
}

export function TalkToLeadButton({ onClick, isActive }: TalkToLeadButtonProps) {
  return (
    <button
      className={styles.fab}
      type="button"
      data-testid="talk-to-lead-button"
      aria-label="Talk to Lead"
      onClick={onClick}
      data-active={isActive ? 'true' : undefined}
      aria-pressed={isActive}
    >
      <svg
        width="20"
        height="20"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth={2}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M8 10h.01M12 10h.01M16 10h.01M9 16H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-5l-5 5v-5z" />
      </svg>
      Talk to Lead
    </button>
  );
}
