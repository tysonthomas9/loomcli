/**
 * Stroke icons for the stacked PR workspace. Decorative only — every icon is
 * aria-hidden and paired with visible text or an accessible label.
 */

const PATHS = {
  stack: "m12 3 9 5-9 5-9-5 9-5ZM3 12l9 5 9-5M3 16l9 5 9-5",
  pr: "M6 7v10m6-12h2a4 4 0 0 1 4 4v8m-6-12 3-3m-3 3 3 3",
  branch: "M6 7v10m12-10a10 10 0 0 1-10 10",
  merge: "M6 7v10m12 0v-2c0-6-12-2-12-8",
  history: "M3 11a9 9 0 1 1 2.5 7M3 5v6h6m3-4v5l3 2",
  check: "m5 12 4 4L19 6",
  checkCircle: "m8 12 3 3 5-6",
  clock: "M12 7v5l3 2",
  chevron: "m7 10 5 5 5-5",
  arrowRight: "M4 12h16m-6-6 6 6-6 6",
  arrowDown: "M12 4v16m-5-5 5 5 5-5",
  search: "m16 16 5 5",
  repo: "M5 4a2 2 0 0 1 2-2h12v19H7a2 2 0 0 1-2-2V4Zm0 14a2 2 0 0 1 2-2h12M9 6h6",
  list: "M8 6h12M8 12h12M8 18h12M3 6h.01M3 12h.01M3 18h.01",
  map: "M4 5v13m0-12h7M4 17h7",
  info: "M12 11v6M12 7h.01",
  help: "M9 8a3 3 0 0 1 6 1c0 2-3 2-3 4m0 4h.01",
  close: "m6 6 12 12M6 18 18 6",
  file: "M14 3H5v18h14V8l-5-5Zm0 0v5h5M8 12h8m-8 4h6",
  warning: "m12 3 10 18H2L12 3Zm0 6v5m0 3h.01",
  link: "m10 13 4-4m-6 6-1 1a4 4 0 0 1-6-6l5-5a4 4 0 0 1 6 0m0 14a4 4 0 0 0 6 0l5-5a4 4 0 0 0-6-6l-1 1",
  refresh: "M20 7a8 8 0 0 0-14-2L3 8m0-5v5h5m-4 9a8 8 0 0 0 14 2l3-3m0 5v-5h-5",
  keyboard:
    "M6 9h.01M10 9h.01M14 9h.01M18 9h.01M6 13h.01M10 13h.01M14 13h.01M18 13h.01M7 16h10",
  spark: "m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5L12 3Z",
  panel: "M15 4v16m-6-10 2 2-2 2",
  dash: "M6 12h12",
  back: "M19 12H5m6-6-6 6 6 6",
  sun: "M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4",
  moon: "M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5Z",
} as const;

export type IconName = keyof typeof PATHS;

/** Extra primitives (circles/rects) per icon, drawn before the path. */
function Extras({ name }: { name: IconName }): JSX.Element | null {
  switch (name) {
    case "pr":
    case "merge":
      return (
        <>
          <circle cx="6" cy="5" r="2" />
          <circle cx="6" cy="19" r="2" />
          <circle cx="18" cy="19" r="2" />
        </>
      );
    case "branch":
      return (
        <>
          <circle cx="6" cy="5" r="2" />
          <circle cx="6" cy="19" r="2" />
          <circle cx="18" cy="5" r="2" />
        </>
      );
    case "checkCircle":
    case "clock":
    case "info":
    case "help":
      return <circle cx="12" cy="12" r="9" />;
    case "search":
      return <circle cx="10.5" cy="10.5" r="6.5" />;
    case "sun":
      return <circle cx="12" cy="12" r="4" />;
    case "map":
      return (
        <>
          <rect x="11" y="3" width="10" height="5" rx="1" />
          <rect x="11" y="15" width="10" height="5" rx="1" />
        </>
      );
    case "keyboard":
      return <rect x="2" y="5" width="20" height="14" rx="2" />;
    case "panel":
      return <rect x="3" y="4" width="18" height="16" rx="2" />;
    default:
      return null;
  }
}

export function Icon({
  name,
  className,
}: {
  name: IconName;
  className?: string | undefined;
}): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      aria-hidden="true"
      focusable="false"
      className={className}
      data-icon={name}
    >
      <Extras name={name} />
      <path d={PATHS[name]} />
    </svg>
  );
}
