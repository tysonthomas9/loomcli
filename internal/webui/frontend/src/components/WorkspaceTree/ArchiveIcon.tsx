/**
 * ArchiveIcon — Lucide-style archive box (lid + body + slot), not a trash can.
 * Shared by the agent row's inline archive button and the agent context menu so
 * the two entry points for the same action can't drift apart.
 */

export interface ArchiveIconProps {
  className?: string | undefined;
}

export function ArchiveIcon({ className }: ArchiveIconProps): JSX.Element {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden
      className={className}
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <rect width="20" height="5" x="2" y="3" rx="1" />
      <path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8" />
      <path d="M10 12h4" />
    </svg>
  );
}
