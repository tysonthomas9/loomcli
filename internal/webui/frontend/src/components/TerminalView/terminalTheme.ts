/**
 * Read xterm.js theme colors from CSS custom properties.
 * CSS variables are defined in styles/variables.css and change with data-theme.
 */
export function getTerminalTheme() {
  const s = getComputedStyle(document.documentElement);
  return {
    background: s.getPropertyValue("--terminal-bg").trim() || "#1e1e1e",
    foreground: s.getPropertyValue("--terminal-fg").trim() || "#d4d4d4",
    cursor: s.getPropertyValue("--terminal-cursor").trim() || "#d4d4d4",
    selectionBackground:
      s.getPropertyValue("--terminal-selection").trim() ||
      "rgba(255, 255, 255, 0.15)",
  };
}
