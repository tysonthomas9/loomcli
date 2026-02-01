/**
 * Chrome Visual Testing Helpers
 *
 * Data attributes and expected states for manual Chrome browser automation testing.
 * Use these selectors with mcp__claude-in-chrome__ tools for consistent verification.
 */

/**
 * CSS selectors for test targeting using data attributes.
 * These match the actual DOM attributes used in the components.
 */
export const TEST_SELECTORS = {
  // ConnectionStatus component
  connectionStatus: '[data-state]',
  connectedState: '[data-state="connected"]',
  connectingState: '[data-state="connecting"]',
  reconnectingState: '[data-state="reconnecting"]',
  disconnectedState: '[data-state="disconnected"]',
  retryButton: '[aria-label="Retry connection now"]',

  // IssueCard component
  issueCard: 'article[data-priority]',
  issueCardByPriority: (priority: number) => `article[data-priority="${priority}"]`,
  blockedCard: 'article[data-blocked="true"]',
  pendingCard: 'article[data-in-pending="true"]',

  // StatusColumn component
  statusColumn: 'section[data-status]',
  columnByStatus: (status: string) => `section[data-status="${status}"]`,
  columnWithItems: 'section[data-has-items="true"]',
  emptyColumn: 'section:not([data-has-items])',

  // Droppable areas
  droppableArea: '[data-droppable-id]',
  activeDropTarget: '[data-is-over="true"]',
} as const;

/**
 * Connection states and their expected visual properties.
 */
export const CONNECTION_STATES = {
  connected: {
    dataState: 'connected',
    indicatorColor: 'green (var(--color-success))',
    text: 'Connected',
    showsRetry: false,
    hasAnimation: false,
  },
  connecting: {
    dataState: 'connecting',
    indicatorColor: 'amber (var(--color-warning))',
    text: 'Connecting...',
    showsRetry: false,
    hasAnimation: true, // pulse animation
  },
  reconnecting: {
    dataState: 'reconnecting',
    indicatorColor: 'amber (var(--color-warning))',
    textPattern: /Reconnecting \(attempt \d+\)\.\.\./,
    showsRetry: true, // after first attempt
    hasAnimation: true, // pulse animation
  },
  disconnected: {
    dataState: 'disconnected',
    indicatorColor: 'red (var(--color-danger))',
    text: 'Disconnected',
    showsRetry: false,
    hasAnimation: false,
  },
} as const;

/**
 * Issue statuses used in the kanban board.
 */
export const ISSUE_STATUSES = [
  'open',
  'in_progress',
  'review',
  'blocked',
  'closed',
] as const;

/**
 * Priority levels and their visual styling.
 */
export const PRIORITY_LEVELS = {
  0: { label: 'P0', className: 'priority0', color: 'red (critical)' },
  1: { label: 'P1', className: 'priority1', color: 'orange (high)' },
  2: { label: 'P2', className: 'priority2', color: 'yellow (medium)' },
  3: { label: 'P3', className: 'priority3', color: 'blue (low)' },
  4: { label: 'P4', className: 'priority4', color: 'gray (backlog)' },
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
  sseEndpoint: '/api/events',
  /** Initial data fetch (regular fetch request) */
  readyEndpoint: '/api/ready',
  /** Issue detail endpoint pattern */
  issueEndpoint: /\/api\/issues\/[\w-]+/,
  /** DevTools Network tab type filter for SSE connections */
  sseNetworkType: 'eventsource',
} as const;

/**
 * Accessibility attributes to verify.
 */
export const A11Y_ATTRIBUTES = {
  connectionStatus: {
    role: 'status',
    ariaLive: 'polite',
    ariaLabelPattern: /Connection status: .+/,
  },
  issueCard: {
    role: 'button', // when clickable
    ariaLabelPattern: /Issue: .+/,
  },
  statusColumn: {
    ariaLabelPattern: /.+ issues/,
  },
  retryButton: {
    ariaLabel: 'Retry connection now',
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
