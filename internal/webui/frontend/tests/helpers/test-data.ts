/**
 * Test data factory functions for E2E tests.
 * Produces valid objects matching the Go backend's JSON contract with sensible defaults.
 * All fields can be overridden via a partial parameter.
 */

import type { Issue, IssueDetails, BlockedIssue, IssueWithDependencyMetadata } from '../../src/types/issue';
import type { Statistics } from '../../src/types/statistics';
import type { MutationPayload, MutationType } from '../../src/api/sse';
import type { Priority } from '../../src/types/common';
import type { Comment } from '../../src/types/comment';

// Auto-incrementing counter for unique IDs within a test file.
// Resets per worker process, preventing cross-file collisions.
let idCounter = 0;

function nextId(): string {
  idCounter++;
  return `test-${String(idCounter).padStart(3, '0')}`;
}

/** Reset the ID counter. Useful in beforeEach hooks. */
export function resetIdCounter(): void {
  idCounter = 0;
}

const NOW = '2026-01-15T10:00:00Z';

export function createIssue(overrides?: Partial<Issue>): Issue {
  const id = overrides?.id ?? nextId();
  return {
    id,
    title: `Test Issue ${id}`,
    status: 'open',
    priority: 2 as Priority,
    issue_type: 'task',
    created_at: NOW,
    updated_at: NOW,
    ...overrides,
  };
}

export function createIssueDetails(overrides?: Partial<IssueDetails>): IssueDetails {
  const id = overrides?.id ?? nextId();
  return {
    id,
    title: `Test Issue ${id}`,
    status: 'open',
    priority: 2 as Priority,
    issue_type: 'task',
    created_at: NOW,
    updated_at: NOW,
    labels: [],
    dependencies: [],
    dependents: [],
    comments: [],
    ...overrides,
  };
}

export function createBlockedIssue(overrides?: Partial<BlockedIssue>): BlockedIssue {
  const id = overrides?.id ?? nextId();
  return {
    id,
    title: `Blocked Issue ${id}`,
    status: 'blocked',
    priority: 2 as Priority,
    issue_type: 'task',
    created_at: NOW,
    updated_at: NOW,
    blocked_by_count: 1,
    blocked_by: ['blocker-001'],
    ...overrides,
  };
}

export function createStats(overrides?: Partial<Statistics>): Statistics {
  return {
    total_issues: 10,
    open_issues: 4,
    in_progress_issues: 2,
    closed_issues: 3,
    blocked_issues: 1,
    deferred_issues: 0,
    ready_issues: 3,
    tombstone_issues: 0,
    pinned_issues: 0,
    epics_eligible_for_closure: 0,
    average_lead_time_hours: 24,
    ...overrides,
  };
}

export function createMutation(overrides?: Partial<MutationPayload>): MutationPayload {
  return {
    type: 'update' as MutationType,
    issue_id: 'test-001',
    timestamp: new Date().toISOString(),
    ...overrides,
  };
}

export function createComment(overrides?: Partial<Comment>): Comment {
  return {
    id: 1,
    issue_id: 'test-001',
    author: 'test-user',
    text: 'Test comment',
    created_at: NOW,
    ...overrides,
  };
}

export function createDependencyMetadata(
  overrides?: Partial<IssueWithDependencyMetadata>,
): IssueWithDependencyMetadata {
  const id = overrides?.id ?? nextId();
  return {
    id,
    title: `Dependency ${id}`,
    status: 'open',
    priority: 2 as Priority,
    issue_type: 'task',
    created_at: NOW,
    updated_at: NOW,
    dependency_type: 'blocks',
    ...overrides,
  };
}

/**
 * Generate N issues with unique IDs and incrementing priorities.
 * Priorities cycle through 0-4.
 */
export function createIssueList(count: number, overrides?: Partial<Issue>): Issue[] {
  return Array.from({ length: count }, (_, i) =>
    createIssue({
      priority: (i % 5) as Priority,
      ...overrides,
    }),
  );
}

/**
 * Creates a full set of test data with issues across all status columns,
 * suitable for rendering a complete kanban board.
 */
export function createKanbanData(): {
  issues: Issue[];
  stats: Statistics;
  blocked: BlockedIssue[];
} {
  const issues: Issue[] = [
    createIssue({ status: 'open', priority: 1 as Priority, title: 'High Priority Open' }),
    createIssue({ status: 'open', priority: 2 as Priority, title: 'Medium Priority Open' }),
    createIssue({ status: 'open', priority: 3 as Priority, title: 'Low Priority Open' }),
    createIssue({ status: 'in_progress', priority: 1 as Priority, title: 'Working On It' }),
    createIssue({ status: 'in_progress', priority: 2 as Priority, title: 'Also In Progress' }),
    createIssue({ status: 'review', priority: 2 as Priority, title: 'Under Review' }),
    createIssue({ status: 'blocked', priority: 1 as Priority, title: 'Blocked Task' }),
    createIssue({ status: 'closed', priority: 3 as Priority, title: 'Done Task' }),
    createIssue({ status: 'closed', priority: 2 as Priority, title: 'Another Done' }),
  ];

  const blocked: BlockedIssue[] = [
    createBlockedIssue({
      id: issues[6]!.id,
      title: issues[6]!.title,
      priority: issues[6]!.priority,
    }),
  ];

  const stats = createStats({
    total_issues: issues.length,
    open_issues: 3,
    in_progress_issues: 2,
    closed_issues: 2,
    blocked_issues: 1,
    ready_issues: 3,
  });

  return { issues, stats, blocked };
}
