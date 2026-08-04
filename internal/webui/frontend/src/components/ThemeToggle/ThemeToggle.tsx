/**
 * ThemeToggle component.
 * Aether wireframe pill: blue track with sun · knob · moon icons.
 */

import type { Theme } from "@/hooks/ui";
import styles from "./ThemeToggle.module.css";

export interface ThemeToggleProps {
  theme: Theme;
  onToggle: () => void;
}

function SunIcon(): JSX.Element {
  return (
    <svg
      className={styles.icon}
      width="12"
      height="12"
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="3" stroke="currentColor" strokeWidth="1.5" />
      <path
        d="M8 1.5V3M8 13v1.5M1.5 8H3M13 8h1.5M3.05 3.05l1.06 1.06M11.89 11.89l1.06 1.06M3.05 12.95l1.06-1.06M11.89 4.11l1.06-1.06"
        stroke="currentColor"
        strokeWidth="1.25"
        strokeLinecap="round"
      />
    </svg>
  );
}

function MoonIcon(): JSX.Element {
  return (
    <svg
      className={styles.icon}
      width="12"
      height="12"
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M11.5 2.8a5.5 5.5 0 1 0 2.7 9.9A5.5 5.5 0 0 1 11.5 2.8Z"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function ThemeToggle({
  theme,
  onToggle,
}: ThemeToggleProps): JSX.Element {
  const isDark = theme === "dark";
  const label = isDark ? "Switch to light mode" : "Switch to dark mode";

  return (
    <button
      className={`${styles.toggle} ${isDark ? styles.dark : ""}`}
      onClick={onToggle}
      aria-label={label}
      title={label}
      aria-pressed={!isDark}
      type="button"
    >
      <SunIcon />
      <span className={styles.knob} aria-hidden="true" />
      <MoonIcon />
    </button>
  );
}
