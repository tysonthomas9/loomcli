# UI Framework & Polish Architecture (Epic a30n9)

## Overview

The UI framework provides the foundational infrastructure for the web UI: design tokens, theme system, responsive layout, keyboard shortcuts, accessibility primitives, real-time data management, and shared UI components. All frontend components consume this framework through CSS custom properties, React context providers, and shared hooks.

Source root: `internal/webui/frontend/src/`

---

## 1. Component Tree Overview

```
<ToastProvider>               <- useToast context (toast notifications)
  <KeyboardShortcutProvider>  <- global shortcut listener + cheatsheet state
    <AppLayout>               <- fixed header, NavRail slot, sidebar slot, <main>
      <LiveRegion />          <- aria-live singletons (polite + assertive)
      <NavRail />             <- icon-only left rail (or bottom tab bar on mobile)
      <main id="main-content">
        {activeView}          <- Kanban / Table / Terminal / ... loaded lazily
      </main>
      <IssueDetailPanel />    <- slide-out panel (usePanelManager controls open/close)
      <AgentDetailPanel />    <- slide-out panel (mutually exclusive with issue panel)
      <KeyboardCheatsheet />  <- portal overlay, ? key
      <ToastContainer />      <- bottom-right, stacked toasts
      <DaemonUnavailableOverlay /> <- full-page overlay when daemon unreachable
      <StaleDataBanner />     <- sticky banner after SSE disconnect > 5s
    </AppLayout>
  </KeyboardShortcutProvider>
</ToastProvider>
```

---

## 2. Design Tokens and CSS Custom Properties

### Token Categories

| Category | Example tokens |
|----------|---------------|
| Background | `--color-bg`, `--color-bg-card`, `--color-bg-secondary` |
| Text | `--color-text`, `--color-text-secondary`, `--color-text-muted` |
| Priority colors | `--color-priority-0` (red) through `--color-priority-4` (gray) |
| Status colors | `--color-status-working`, `--color-status-done`, etc. |
| Diff colors | `--color-diff-add`, `--color-diff-del`, `--color-diff-hunk` |
| Skeleton loading | `--color-skeleton-bg`, `--color-skeleton-shimmer` |
| Terminal | `--terminal-bg`, `--terminal-fg`, `--terminal-cursor`, etc. |
| Z-index layers | `--z-dropdown` (100) through `--z-toast` (500) |
| Spacing | `--space-1` (4px) through `--space-12` (48px) |
| Transitions | `--transition-fast` (150ms), `--transition-normal` (200ms) |

### CSS Import Order

`styles/index.css` enforces load order:
```css
@import "./fonts.css";     /* Inter woff2 @font-face */
@import "./reset.css";     /* Modern CSS reset */
@import "./variables.css"; /* All design tokens */
@import "./base.css";      /* Element styles using token references */
```

### CSS Methodology

CSS Modules (`*.module.css`) used exclusively. No global utility classes, no inline styles except for dynamic values. State modifiers use `data-` attributes on the element:
```css
.navButton[data-active="true"] { /* active state */ }
.backendSelector[data-saving]  { /* saving state */ }
```

---

## 3. Theme System

### Architecture: Dark-First, Attribute-Switch

`:root` defines the dark palette; `[data-theme="light"]` on `<html>` overrides for light mode.

```css
:root {
  --bg-color: #1a1a2e;
  --bg-card: #232336;
  --color-text: #e2e2e8;
  /* ~100 tokens */
}

[data-theme="light"] {
  --bg-color: #e6e4dc;
  --bg-card: #f5f4f1;
  --color-text: #1f2937;
  /* overrides only */
}
```

### `useTheme` Hook

**File**: `hooks/useTheme.ts`

```typescript
export type Theme = "light" | "dark";

export function useTheme(): UseThemeReturn {
  // Priority: localStorage("cortex:theme") -> OS prefers-color-scheme -> "dark"
  const [theme, setThemeState] = useState<Theme>(() => getStoredTheme() ?? getOSTheme());
  const [hasExplicit, setHasExplicit] = useState(() => getStoredTheme() !== null);
  // Apply: document.documentElement.dataset.theme = theme
  // OS changes only apply when hasExplicit is false
  return { theme, toggleTheme, setTheme };
}
```

- `applyTheme` sets both `document.documentElement.dataset.theme` and `document.documentElement.style.colorScheme`
- OS changes only apply when no user override stored
- localStorage key: `"cortex:theme"` (migrated from V5 `"theme-preference"`)

### `ThemeToggle` Component

**File**: `components/ThemeToggle/ThemeToggle.tsx`

Stateless display component. Sun SVG in dark mode, moon SVG in light mode. Dynamic `aria-label`: "Switch to light mode" / "Switch to dark mode".

---

## 4. Responsive Layout

### Breakpoints

| Token | Value | Usage |
|-------|-------|-------|
| `--breakpoint-tablet` | 1024px | Compact header padding, smaller title |
| `--breakpoint-mobile` | 768px | NavRail -> bottom tab bar |
| `--breakpoint-small` | 640px | Further header compression |
| `--touch-target-min` | 44px | NavRail button height on mobile |
| `--nav-bottom-height` | 56px | Bottom nav clearance on mobile |
| `--nav-rail-width` | 58px | Left rail width on desktop |
| `--unified-header-height` | 60px | Fixed header height |

### AppLayout Structure

- `body`: `height: 100vh; overflow: hidden; display: flex; flex-direction: column`
- `.header`: `position: fixed; top: 0; height: 60px; z-index: var(--z-sticky)`
- `.contentWrapper`: `flex: 1; display: flex; padding-top: 60px`
- `.main`: `flex: 1; overflow: auto` (scrollable content, id="main-content")

### NavRail Responsive Behavior

```css
/* Desktop: vertical left rail (58px wide) */
.navRail { flex-direction: column; width: 58px; border-right: 1px solid var(--color-border); }

/* Mobile @media (width <= 768px): horizontal bottom tab bar */
.navRail { flex-direction: row; width: 100%; height: 56px; position: fixed; bottom: 0; }
.contentWrapper { padding-bottom: 56px; }
```

### Header Adaptive Compression

| Breakpoint | Change |
|------------|--------|
| <= 1024px | gap 20px -> 16px; padding 24px -> 16px |
| <= 640px | gap -> 12px; padding -> 12px; title 2rem -> 1.5rem |
| <= 480px | title 1.5rem -> 1.3rem; `.brand` min-width: 0 |

### Reduced Motion

```css
@media (prefers-reduced-motion: reduce) {
  .skeleton { animation: none; }
  .toggle   { transition: none; }
  .skipLink { transition: none; }
}
```

---

## 5. Keyboard Shortcuts and Escape Layering

### Architecture

Two independent mechanisms:

**A. Global shortcut provider** — handles view switching, Cmd/Ctrl+K, `?`, arrow delegation.

**B. Escape layer registry** — module-level sorted array of layers. Works without the provider.

### Layer Priority Constants

```typescript
export const LAYER_CONFIRM_DIALOG      = 60;
export const LAYER_TOAST               = 50;
export const LAYER_CHEATSHEET          = 45;
export const LAYER_WORKSPACE_SWITCHER  = 42;
export const LAYER_MODAL               = 40;
export const LAYER_TERMINAL_PANEL      = 30;
export const LAYER_AGENT_PANEL         = 20;
export const LAYER_ISSUE_PANEL         = 10;
export const LAYER_TERMINAL_SEARCH     =  5;
```

Higher priority closes first. At most one layer closes per Escape keypress.

### `useRegisterEscapeLayer` Hook

```typescript
export function useRegisterEscapeLayer(
  priority: number,
  handler: () => void,
  active: boolean,
): void
```

Registers when `active` becomes true; unregisters when false or on unmount. Uses a stable wrapper that reads `handlerRef.current`. Module-level listener attached when first layer registers, detached when last unregisters.

### KeyboardShortcutProvider Bindings

| Key | Condition | Action |
|-----|-----------|--------|
| `1` | not in input | Switch to kanban |
| `2` | not in input | Switch to table |
| `3` | not in input | Switch to terminal |
| `4` | not in input | Switch to observability |
| `5` | not in input | Switch to files |
| `6` | not in input | Switch to workspace |
| `0` | not in input | Switch to settings |
| `?` | not in input | Toggle keyboard cheatsheet |
| `Cmd/Ctrl+K` | always | Workspace switcher or search focus |
| `Cmd/Ctrl+Shift+1-9` | always | Switch to workspace by position |
| `ArrowUp/Down/Left/Right` | not in input | Delegated to `onArrowNav` |
| `Escape` | always | Top escape layer handler |

**Input suppression**: Shortcuts blocked when focus is inside `INPUT`, `TEXTAREA`, `SELECT`, `[contenteditable="true"]`, `.cm-editor`, or `.xterm`.

---

## 6. Accessibility

### Skip Link

`AppLayout` renders a visually hidden skip link as the first child:
```html
<a href="#main-content" class="skipLink">Skip to main content</a>
```
Uses `transform: translateY(-100%)` off-screen and `translateY(var(--space-2))` when focused.

### Global Focus Ring

```css
:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
:focus:not(:focus-visible) { outline: none; }
```

### LiveRegion Singleton

**File**: `components/LiveRegion/LiveRegion.tsx`

Mounted once inside `AppLayout`. Two hidden `aria-live` containers:
- `aria-live="polite"` + `role="status"` — non-urgent
- `aria-live="assertive"` — urgent (connection loss, errors)

Uses clear-then-set in `requestAnimationFrame` so identical repeated messages trigger re-announcement.

### `useAnnounce` Hook

```typescript
export function useAnnounce(debounceMs = 150): {
  announce: (message: string, priority?: "polite" | "assertive") => void;
}
```

Module-level `EventTarget` for cross-component communication. 150ms debounce coalesces rapid announcements.

### `useFocusTrap` Hook

```typescript
export function useFocusTrap(
  containerRef: React.RefObject<HTMLElement | null>,
  isActive: boolean,
  options?: { initialFocus?: React.RefObject<HTMLElement | null>; activationDelay?: number; }
): void
```

Re-queries focusable elements on each Tab press. Wraps at boundaries. Uses `getFocusableElements(container)` from `utils/focusUtils.ts`.

### `useFocusReturn` Hook

```typescript
export function useFocusReturn(
  isOpen: boolean,
  options?: { focusTarget?: Ref; focusDelay?: number; fallbackRef?: Ref; }
): void
```

Captures `document.activeElement` when `isOpen` becomes true, restores on false. Handles edge case where trigger element has been removed from DOM.

### Panel Focus Management

Both `IssueDetailPanel` and `AgentDetailPanel` compose:
```typescript
useRegisterEscapeLayer(LAYER_ISSUE_PANEL, closePanel, isOpen);
useFocusReturn(isOpen, { focusDelay: 300 });
useFocusTrap(panelRef, isOpen, { activationDelay: 50 });
```

---

## 7. Real-Time Data (SSE)

### SSE Architecture

**Files**: `api/sse.ts` + `hooks/useSSE.ts`

Server-Sent Events for push-based real-time updates. Connection states:
```typescript
export type ConnectionState = "disconnected" | "connecting" | "connected" | "reconnecting";
```

### `useSSE` Hook

```typescript
const { state, isConnected, reconnectAttempts, lastEventId, retryNow } = useSSE({
  autoConnect: true,
  since: Date.now(),
  sourceRepos: ["repo-a"],
  onMutation: (mutation) => { ... },
  onError: (error) => { ... },
  onStateChange: (state) => { ... },
});
```

- Callbacks stored in refs to avoid stale closures
- `sourceRepos` reconnects when the repo set changes
- `beforeunload` flag suppresses false-positive error events during page navigation

### Reconnect Backoff

**File**: `utils/reconnectBackoff.ts`

```
delay = min(baseDelay * 2^attempt, maxDelay) * jitter
jitter = [1 - jitterFactor/2, 1 + jitterFactor/2]
```

Default config: base 1000ms, max 30000ms, 10 attempts, +/-25% jitter.

### Stale Data Banner

**File**: `components/StaleDataBanner/StaleDataBanner.tsx`

Shown when SSE disconnected for more than 5 seconds. `role="alert"`, `aria-live="assertive"`. Displays live elapsed counter updated every second. Retry button when `connectionLost && onRetry`.

### Optimistic Updates with Rollback

**File**: `hooks/useOptimisticUpdate.ts`

```typescript
const handle = startOptimistic(issueId, snapshot);
// Apply optimistic UI update immediately
// SSE mutations for this issue are buffered during optimistic window
handle.confirm();   // flushes buffered SSE mutations in order
handle.rollback("Update failed"); // restores snapshot + error toast
```

Lifecycle:
1. `startOptimistic` saves pre-change snapshot, starts 30-second auto-rollback timer
2. Incoming SSE mutations for the optimistic issue are buffered
3. `confirm()` flushes buffered mutations in order
4. `rollback()` restores snapshot, replays buffered mutations, shows error toast
5. Auto-rollback at 30s if neither confirm nor rollback was called

---

## 8. Shared UI Primitives

### Toast Notifications

**File**: `hooks/useToast.tsx`

`ToastProvider` wraps the application root. Uses `useReducer` with ADD, REMOVE, CLEAR actions.

```typescript
const { showToast, dismissToast, dismissAll } = useToast();
showToast("Issue closed", { type: "success" });
showToast("Failed to save", { type: "error", duration: 10000 });
showToast("Item deleted", { type: "warning", onUndo: () => restore() });
```

| Type | Auto-dismiss |
|------|-------------|
| `success` | 3000ms |
| `error` | 10000ms |
| `warning` | 5000ms |
| `info` | 5000ms |

**Coalescing**: Duplicate toasts with same `type + message` within 500ms are silently dropped.

**maxToasts**: Default 3. Oldest toasts dismissed when exceeded.

**Undo pattern**: Destructive operations call `showToast` with `onUndo`. Toast renders "Undo" button.

### Loading Skeleton

**File**: `components/LoadingSkeleton/LoadingSkeleton.tsx`

Shimmer-animated placeholder system. Base shapes: `rect`, `text`, `circle`.

10 preset variants: `Card`, `Column`, `Graph`, `Monitor`, `DetailPanel`, `Table`, `FileExplorer`, `Terminal`, `Observability`, `AgentDetail`. All include `aria-hidden="true"`.

### Daemon Unavailable Overlay

**File**: `components/DaemonUnavailableOverlay/DaemonUnavailableOverlay.tsx`

Full-page blocking overlay when daemon unreachable. Focus trap enforced.

Powered by `useDaemonHealth`:
- Polls `GET /api/health` with exponential backoff (5s -> 60s cap)
- 2-second debounce before showing overlay
- Listens for `"daemon-unavailable"` custom events from `fetchApi` on 503
- Refetches on `visibilitychange`

### Empty State

**File**: `components/EmptyState/EmptyState.tsx`

Three variants: `no-workspaces`, `no-issues`, `no-agents`. Each renders SVG icon, heading, and code-snippet hint.

### Connection Status

**File**: `components/ConnectionStatus/ConnectionStatus.tsx`

Badge driven by `ConnectionState`. Colored dot with text. "Retry Now" button after first failed reconnect.

---

## 9. Search

### SearchInput Component

**File**: `components/search/SearchInput.tsx`

Supports controlled and uncontrolled modes. `type="search"` input. Clear button when non-empty. Escape clears value. `forwardRef` for programmatic focus.

### Result Highlighting

**Files**: `components/HighlightText/HighlightText.tsx` + `contexts/SearchTermContext.tsx`

```typescript
<SearchTermProvider value={debouncedSearch}>
  {issues.map(issue => <IssueCard key={issue.id} issue={issue} />)}
</SearchTermProvider>

// Inside IssueCard (no prop drilling):
const searchTerm = useSearchTerm();
<HighlightText text={issue.title} searchTerm={searchTerm} />
```

`HighlightText` splits on case-insensitive regex and wraps matches in `<mark>`.

---

## 10. Navigation and State

### URL Parameter Map

All navigation state encoded in `window.location.search`. No React Router — direct `replaceState` / `pushState`.

| Parameter | Hook | Values |
|-----------|------|--------|
| `view` | `useViewState` | kanban, table, graph, terminal, etc. |
| `issue` | `useViewState` | issue ID |
| `workspace` | `useWorkspaceParam` | workspace name |
| `repos` | `useRepoFilter` | comma-separated repo names |
| `priority` | `useFilterState` | 0-4 |
| `type` | `useFilterState` | bug, feature, task, epic, chore |
| `labels` | `useFilterState` | comma-separated label names |
| `search` | `useFilterState` | free text |
| `showBlocked` | `useFilterState` | "true" |
| `groupBy` | `useFilterState` | none, epic, assignee, priority, etc. |

### `useViewState`

- `setView()`: `replaceState` (no history entry)
- `navigateToView()`: `pushState` (enables browser back)
- `urlIssueId` extracted from `?issue=` param
- `popstate` listener for browser back/forward

### `usePanelManager`

Panel mutual exclusivity:
- Same type, different content: immediate swap
- Different type: 300ms close animation, then open
- Same panel: no-op
- Rapid clicks: pending panel replaced

### `useWorkspaceState`

On workspace switch: capture snapshot -> close panels -> restore snapshot via `requestAnimationFrame`. `WorkspaceSnapshot` captures: view, filters, searchValue, selectedIssueId, scrollTop.

### `useScrollRestore`

Module-level `Map<viewKey, number>`. Saves `scrollTop` on unmount, restores via `requestAnimationFrame` on mount.

---

## 11. localStorage Management

### V5 -> V6 Migration

**File**: `utils/migrateLocalStorage.ts`

Runs synchronously at app boot before React renders. Idempotent via version stamp.

| V5 key | V6 key |
|--------|--------|
| `theme-preference` | `cortex:theme` |
| `beads-recent-assignees` | `cortex:recent-assignees` |
| `terminal-font-family` | `cortex:terminal-font-family` |
| `terminal-font-size` | `cortex:terminal-font-size` |

All V6 keys use the `cortex:` namespace prefix. Handles `QuotaExceededError`.

---

## 12. Performance

### Virtualized Lists

**File**: `hooks/useVirtualList.ts`

Thin wrapper around `@tanstack/react-virtual`'s `useVirtualizer`:

```typescript
const { virtualItems, totalSize, measureElement } = useVirtualList({
  count: issues.length,
  scrollContainerRef,
  estimatedSize: 96,
  overscan: 5,
  measureElements: true,
});
```

With `measureElements: true`, uses `getBoundingClientRect().height` for actual sizes. Used in table view with variable row heights.

---

## 13. File Map

### Styles

| File | Role |
|------|------|
| `styles/variables.css` | All design tokens (dark defaults + light overrides) |
| `styles/reset.css` | Modern CSS reset |
| `styles/base.css` | Element styles using token references |
| `styles/fonts.css` | Inter woff2 @font-face declarations |
| `styles/index.css` | Import orchestration |

### Hooks

| File | Role |
|------|------|
| `hooks/useTheme.ts` | Dark/light theme, OS detection, localStorage persistence |
| `hooks/useKeyboardShortcuts.tsx` | Global shortcuts, Escape layer registry, cheatsheet state |
| `hooks/usePanelManager.ts` | Panel mutual exclusivity and transition choreography |
| `hooks/useViewState.ts` | View mode URL sync, pushState/replaceState, popstate |
| `hooks/useWorkspaceParam.ts` | `?workspace=` URL param sync |
| `hooks/useWorkspaceState.ts` | Per-workspace UI snapshot on switch |
| `hooks/useFilterState.ts` | Multi-key filter URL sync |
| `hooks/useRepoFilter.ts` | `?repos=` URL param sync |
| `hooks/useSSE.ts` | SSE connection lifecycle wrapper |
| `hooks/useOptimisticUpdate.ts` | Optimistic state with SSE buffering and rollback |
| `hooks/useDebounce.ts` | Generic debounce |
| `hooks/useIssueSearch.ts` | Client-side issue search with module-level cache |
| `hooks/useSearchScope.ts` | Search scope context |
| `hooks/useVirtualList.ts` | @tanstack/react-virtual wrapper |
| `hooks/useScrollRestore.ts` | Per-view scroll position persistence |
| `hooks/useSplitRatio.ts` | Panel split ratio with localStorage persistence |
| `hooks/useAnnounce.ts` | aria-live announcement dispatch |
| `hooks/useFocusTrap.ts` | Tab key focus containment |
| `hooks/useFocusReturn.ts` | Focus restoration on close |
| `hooks/useToast.tsx` | Toast context + reducer + coalescing |
| `hooks/useDaemonHealth.ts` | Daemon availability polling + backoff |
| `hooks/useBackends.ts` | Backend health data fetch + merge |

### Components

| File | Role |
|------|------|
| `components/AppLayout/AppLayout.tsx` | Top-level shell, responsive header slots |
| `components/NavRail/NavRail.tsx` | Icon navigation, mobile tab bar |
| `components/ThemeToggle/ThemeToggle.tsx` | Sun/moon toggle button |
| `components/KeyboardCheatsheet/KeyboardCheatsheet.tsx` | Shortcut overlay via portal |
| `components/LoadingSkeleton/LoadingSkeleton.tsx` | Shimmer placeholders, 10 presets |
| `components/StaleDataBanner/StaleDataBanner.tsx` | SSE stale data indicator |
| `components/DaemonUnavailableOverlay/DaemonUnavailableOverlay.tsx` | Blocking overlay with focus trap |
| `components/EmptyState/EmptyState.tsx` | First-run + empty data variants |
| `components/ConnectionStatus/ConnectionStatus.tsx` | SSE state badge |
| `components/LiveRegion/LiveRegion.tsx` | aria-live singletons |
| `components/Toast/Toast.tsx` | Individual toast with type icon |
| `components/Toast/ToastContainer.tsx` | Stacked toast container |
| `components/search/SearchInput.tsx` | Search input with clear button |
| `components/HighlightText/HighlightText.tsx` | Mark-tag result highlighting |
| `components/FilterBar/FilterBar.tsx` | Priority, type, label, repo, group-by dropdowns |
| `components/BackendSelectorDropdown/BackendSelectorDropdown.tsx` | Searchable backend dropdown |
| `components/BackendSelectorDropdown/backendDefaults.ts` | Known backend metadata |

### Contexts

| File | Role |
|------|------|
| `contexts/SearchTermContext.tsx` | SearchTermProvider + useSearchTerm |
| `contexts/IssueSessionContext.tsx` | Issue-to-session mapping context |

### Utilities

| File | Role |
|------|------|
| `utils/migrateLocalStorage.ts` | V5->V6 key migration, run at boot |
| `utils/focusUtils.ts` | getFocusableElements, isFocusable |
| `utils/reconnectBackoff.ts` | Exponential backoff with jitter |
| `utils/escapeRegex.ts` | Escape special regex chars for search |
