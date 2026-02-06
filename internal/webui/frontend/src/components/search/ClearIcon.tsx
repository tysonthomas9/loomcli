/**
 * ClearIcon component.
 * X/close SVG icon for clearing input.
 */

interface ClearIconProps {
  /** Icon size in pixels (default: 16) */
  size?: number;
  /** Additional CSS class name */
  className?: string | undefined;
}

/**
 * ClearIcon renders an X/close SVG icon.
 * Uses currentColor for fill, allowing it to inherit text color.
 */
export function ClearIcon({ size = 16, className }: ClearIconProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <line x1="18" y1="6" x2="6" y2="18" />
      <line x1="6" y1="6" x2="18" y2="18" />
    </svg>
  );
}
