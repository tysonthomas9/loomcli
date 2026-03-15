/**
 * Chrome Visual Testing Helpers
 *
 * Selectors and expected states for browser automation testing via MCP tools.
 * Designed for use with the `mcp__claude-in-chrome__` MCP server, which enables
 * Claude to interact with the running web UI in a real Chrome browser.
 *
 * ## When to use
 * - Manual QA verification of visual states (connection status, priorities, etc.)
 * - Automated browser tests driven by Claude via MCP
 * - Debugging UI state by querying data attributes
 *
 * ## Example usage with MCP browser tools
 * ```
 * // Check connection status
 * mcp__claude-in-chrome__querySelector('[data-state="connected"]')
 *
 * // Find all blocked issue cards
 * mcp__claude-in-chrome__querySelectorAll('article[data-blocked="true"]')
 *
 * // Find issues in a specific column
 * mcp__claude-in-chrome__querySelector('section[data-status="in_progress"]')
 *
 * // Check a P0 critical issue exists
 * mcp__claude-in-chrome__querySelector('article[data-priority="0"]')
 * ```
 *
 * ## Keeping in sync
 * These selectors mirror data attributes set in the actual React components.
 * If component data attributes change, update the corresponding selectors here.
 */

/**
 * CSS selectors for test targeting using data attributes.
 * These match the actual DOM attributes used in the components.
 */
export const TEST_SELECTORS = {
  // ConnectionStatus component
  connectionStatus: "[data-state]",
  connectedState: '[data-state="connected"]',
  connectingState: '[data-state="connecting"]',
  reconnectingState: '[data-state="reconnecting"]',
  disconnectedState: '[data-state="disconnected"]',
  retryButton: '[aria-label="Retry connection now"]',

  // IssueCard component
  issueCard: "article[data-priority]",
  issueCardByPriority: (priority: number) =>
    `article[data-priority="${priority}"]`,
  blockedCard: 'article[data-blocked="true"]',
  pendingCard: 'article[data-in-pending="true"]',

  // StatusColumn component
  statusColumn: "section[data-status]",
  columnByStatus: (status: string) => `section[data-status="${status}"]`,
  columnWithItems: 'section[data-has-items="true"]',
  emptyColumn: "section:not([data-has-items])",

  // Droppable areas
  droppableArea: "[data-droppable-id]",
  activeDropTarget: '[data-is-over="true"]',
} as const;

/**
 * Connection states and their expected visual properties.
 */
export const CONNECTION_STATES = {
  connected: {
    dataState: "connected",
    indicatorColor: "green (var(--color-success))",
    text: "Connected",
    showsRetry: false,
    hasAnimation: false,
  },
  connecting: {
    dataState: "connecting",
    indicatorColor: "amber (var(--color-warning))",
    text: "Connecting...",
    showsRetry: false,
    hasAnimation: true, // pulse animation
  },
  reconnecting: {
    dataState: "reconnecting",
    indicatorColor: "amber (var(--color-warning))",
    textPattern: /Reconnecting \(attempt \d+\)\.\.\./,
    showsRetry: true, // after first attempt
    hasAnimation: true, // pulse animation
  },
  disconnected: {
    dataState: "disconnected",
    indicatorColor: "red (var(--color-danger))",
    text: "Disconnected",
    showsRetry: false,
    hasAnimation: false,
  },
} as const;

/**
 * Issue statuses used in the kanban board.
 */
export const ISSUE_STATUSES = [
  "open",
  "in_progress",
  "review",
  "blocked",
  "closed",
] as const;

/**
 * Priority levels and their visual styling.
 */
export const PRIORITY_LEVELS = {
  0: { label: "P0", className: "priority0", color: "red (critical)" },
  1: { label: "P1", className: "priority1", color: "orange (high)" },
  2: { label: "P2", className: "priority2", color: "yellow (medium)" },
  3: { label: "P3", className: "priority3", color: "blue (low)" },
  4: { label: "P4", className: "priority4", color: "gray (backlog)" },
} as const;

/**
 * Expected timing for SSE updates.
 *
 * Note: SSE uses browser-native EventSource reconnection, not application-level heartbeats.
 * The browser handles reconnection automatically when the connection closes.
 */
export const TIMING_EXPECTATIONS = {
  /** Updates should arrive within this many ms of mutation */
  updateLatency: 200,
  /** Browser's EventSource reconnection delay (approximate) */
  browserReconnectDelayMs: 3000,
  /** Initial application reconnect delay (for useSSE hook) */
  reconnectDelayMs: 1000,
  /** Maximum reconnect delay with backoff */
  maxReconnectDelayMs: 30000,
} as const;

/**
 * Network patterns to look for during testing.
 *
 * Note: In Chrome DevTools Network tab, SSE connections appear with type "eventsource"
 * and remain open (streaming) rather than completing immediately like regular requests.
 */
export const NETWORK_PATTERNS = {
  /** SSE endpoint for real-time events (shows as "eventsource" type in DevTools) */
  sseEndpoint: "/api/events",
  /** Initial data fetch (regular fetch request) */
  readyEndpoint: "/api/ready",
  /** Issue detail endpoint pattern */
  issueEndpoint: /\/api\/issues\/[\w-]+/,
  /** DevTools Network tab type filter for SSE connections */
  sseNetworkType: "eventsource",
} as const;

/**
 * Accessibility attributes to verify.
 */
export const A11Y_ATTRIBUTES = {
  connectionStatus: {
    role: "status",
    ariaLive: "polite",
    ariaLabelPattern: /Connection status: .+/,
  },
  issueCard: {
    role: "button", // when clickable
    ariaLabelPattern: /Issue: .+/,
  },
  statusColumn: {
    ariaLabelPattern: /.+ issues/,
  },
  retryButton: {
    ariaLabel: "Retry connection now",
  },
  navRail: {
    containerRole: "navigation",
    ariaLabel: "Primary",
  },
  bulkActionToolbar: {
    role: "toolbar",
    ariaLabelPattern: /Bulk actions for \d+ selected issue/,
  },
  priorityDropdown: {
    ariaHasPopup: "listbox",
    ariaLabelPattern: /Priority: P\d - .+/,
  },
  liveRegion: {
    politeTestId: "live-region-polite",
    assertiveTestId: "live-region-assertive",
    politeAriaLive: "polite",
    assertiveAriaLive: "assertive",
  },
} as const;

/**
 * Type for connection state keys.
 */
export type ConnectionStateKey = keyof typeof CONNECTION_STATES;

/**
 * Type for issue status values.
 */
export type IssueStatus = (typeof ISSUE_STATUSES)[number];

/**
 * Type for priority levels.
 */
export type PriorityLevel = keyof typeof PRIORITY_LEVELS;
