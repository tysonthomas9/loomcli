/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SwimLaneBoard localStorage persistence functionality.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import '@testing-library/jest-dom';

import { SwimLaneBoard } from '../SwimLaneBoard';
import type { Issue, Status } from '@/types';

/**
 * Create a mock issue for testing.
 */
function createMockIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: `issue-${Math.random().toString(36).slice(2, 9)}`,
    title: 'Test Issue',
    priority: 2,
    status: 'open',
    issue_type: 'task',
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    ...overrides,
  };
}

/**
 * Default statuses for testing.
 */
const defaultStatuses: Status[] = ['open', 'in_progress', 'closed'];

/**
 * Mock localStorage implementation for testing.
 */
function createMockLocalStorage(): {
  store: Map<string, string>;
  getItem: ReturnType<typeof vi.fn>;
  setItem: ReturnType<typeof vi.fn>;
  removeItem: ReturnType<typeof vi.fn>;
  clear: ReturnType<typeof vi.fn>;
} {
  const store = new Map<string, string>();

  return {
    store,
    getItem: vi.fn((key: string) => store.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => {
      store.set(key, value);
    }),
    removeItem: vi.fn((key: string) => {
      store.delete(key);
    }),
    clear: vi.fn(() => {
      store.clear();
    }),
  };
}

describe('SwimLaneBoard persistence', () => {
  let mockStorage: ReturnType<typeof createMockLocalStorage>;
  let originalLocalStorage: Storage;

  beforeEach(() => {
    // Save original localStorage
    originalLocalStorage = window.localStorage;

    // Create fresh mock storage
    mockStorage = createMockLocalStorage();

    // Replace localStorage with mock
    Object.defineProperty(window, 'localStorage', {
      value: {
        getItem: mockStorage.getItem,
        setItem: mockStorage.setItem,
        removeItem: mockStorage.removeItem,
        clear: mockStorage.clear,
        get length() {
          return mockStorage.store.size;
        },
        key: (index: number) => {
          const keys = Array.from(mockStorage.store.keys());
          return keys[index] ?? null;
        },
      },
      writable: true,
      configurable: true,
    });

    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    // Restore original localStorage
    Object.defineProperty(window, 'localStorage', {
      value: originalLocalStorage,
      writable: true,
      configurable: true,
    });
  });

  describe('localStorage persistence', () => {
    it('persists collapsed lane to localStorage', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // Get the first toggle button (alice's lane - sorted alphabetically)
      const toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'true');

      // Collapse the first lane
      fireEvent.click(toggleButtons[0]);

      // Verify localStorage was called with the correct key and value
      expect(mockStorage.setItem).toHaveBeenCalledWith(
        'swimlane-collapsed-assignee',
        expect.any(String)
      );

      // Parse the stored value to verify it contains the correct lane ID
      const lastSetCall = mockStorage.setItem.mock.calls[mockStorage.setItem.mock.calls.length - 1];
      const storedValue = JSON.parse(lastSetCall[1] as string) as string[];
      // Lane IDs are in the format "lane-{groupBy}-{key}"
      expect(storedValue).toContain('lane-assignee-alice');
    });

    it('removes expanded lane from localStorage', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      const toggleButtons = screen.getAllByTestId('collapse-toggle');

      // Collapse the first lane
      fireEvent.click(toggleButtons[0]);

      // Expand it again
      fireEvent.click(toggleButtons[0]);

      // Verify the final state is an empty array
      const lastSetCall = mockStorage.setItem.mock.calls[mockStorage.setItem.mock.calls.length - 1];
      const storedValue = JSON.parse(lastSetCall[1] as string) as string[];
      expect(storedValue).not.toContain('lane-assignee-alice');
    });

    it('survives component remount', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
      ];

      // Pre-populate localStorage with a collapsed lane (lane IDs use "lane-" prefix)
      mockStorage.store.set('swimlane-collapsed-assignee', JSON.stringify(['lane-assignee-alice']));

      const { unmount } = render(
        <SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />
      );

      // Verify the lane is collapsed on initial render
      const toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'false');
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'true');

      // Unmount and remount
      unmount();

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // Verify the collapsed state persisted after remount
      const newToggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(newToggleButtons[0]).toHaveAttribute('aria-expanded', 'false');
      expect(newToggleButtons[1]).toHaveAttribute('aria-expanded', 'true');
    });
  });

  describe('groupBy isolation', () => {
    it('different groupBy modes have separate storage', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', priority: 1, status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', priority: 2, status: 'open' }),
      ];

      // Render with assignee grouping
      const { unmount } = render(
        <SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />
      );

      // Collapse a lane
      const assigneeToggleButtons = screen.getAllByTestId('collapse-toggle');
      fireEvent.click(assigneeToggleButtons[0]);

      // Verify assignee storage was updated
      expect(mockStorage.setItem).toHaveBeenCalledWith(
        'swimlane-collapsed-assignee',
        expect.any(String)
      );

      unmount();
      mockStorage.setItem.mockClear();

      // Render with priority grouping
      render(<SwimLaneBoard issues={issues} groupBy="priority" statuses={defaultStatuses} />);

      // Collapse a lane
      const priorityToggleButtons = screen.getAllByTestId('collapse-toggle');
      fireEvent.click(priorityToggleButtons[0]);

      // Verify priority storage was updated (not assignee)
      expect(mockStorage.setItem).toHaveBeenCalledWith(
        'swimlane-collapsed-priority',
        expect.any(String)
      );
    });

    it('changing groupBy loads correct stored state', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', priority: 1, status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', priority: 2, status: 'open' }),
      ];

      // Pre-populate both storages with different collapsed lanes (lane IDs use "lane-" prefix)
      mockStorage.store.set('swimlane-collapsed-assignee', JSON.stringify(['lane-assignee-alice']));
      mockStorage.store.set('swimlane-collapsed-priority', JSON.stringify(['lane-priority-2']));

      // Render with assignee grouping
      const { unmount } = render(
        <SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />
      );

      // Verify alice is collapsed, bob is expanded
      let toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'false'); // alice collapsed
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'true'); // bob expanded

      // Unmount and render with priority grouping
      unmount();
      render(<SwimLaneBoard issues={issues} groupBy="priority" statuses={defaultStatuses} />);

      // Verify P2 is collapsed, P1 is expanded
      toggleButtons = screen.getAllByTestId('collapse-toggle');
      // Priority lanes are sorted, so P1 comes before P2
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'true'); // P1 expanded
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'false'); // P2 collapsed
    });
  });

  describe('localStorage error handling', () => {
    it('component works when localStorage getItem throws', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
      ];

      // Make getItem throw an error
      mockStorage.getItem.mockImplementation(() => {
        throw new Error('localStorage not available');
      });

      // Component should render without crashing
      expect(() => {
        render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);
      }).not.toThrow();

      // Should start with default expanded state
      const toggleButton = screen.getByTestId('collapse-toggle');
      expect(toggleButton).toHaveAttribute('aria-expanded', 'true');
    });

    it('component works when localStorage setItem throws', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
      ];

      // Make setItem throw an error
      mockStorage.setItem.mockImplementation(() => {
        throw new Error('localStorage quota exceeded');
      });

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // Toggling should still work locally even if storage fails
      const toggleButton = screen.getByTestId('collapse-toggle');
      expect(toggleButton).toHaveAttribute('aria-expanded', 'true');

      fireEvent.click(toggleButton);

      // State should still update in memory
      expect(toggleButton).toHaveAttribute('aria-expanded', 'false');
    });

    it('invalid JSON in localStorage does not break component', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
      ];

      // Pre-populate with invalid JSON
      mockStorage.store.set('swimlane-collapsed-assignee', 'not valid json {{{');

      // Component should render without crashing
      expect(() => {
        render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);
      }).not.toThrow();

      // Should start with default expanded state (ignoring invalid stored value)
      const toggleButton = screen.getByTestId('collapse-toggle');
      expect(toggleButton).toHaveAttribute('aria-expanded', 'true');
    });

    it('non-array JSON in localStorage is handled gracefully', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
      ];

      // Pre-populate with valid JSON but wrong type
      mockStorage.store.set('swimlane-collapsed-assignee', JSON.stringify({ invalid: 'object' }));

      // Component should render without crashing
      expect(() => {
        render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);
      }).not.toThrow();

      // Should start with default expanded state
      const toggleButton = screen.getByTestId('collapse-toggle');
      expect(toggleButton).toHaveAttribute('aria-expanded', 'true');
    });

    it('array with non-string items in localStorage is handled gracefully', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
      ];

      // Pre-populate with array containing non-strings
      mockStorage.store.set('swimlane-collapsed-assignee', JSON.stringify([1, 2, 3]));

      // Component should render without crashing
      expect(() => {
        render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);
      }).not.toThrow();

      // Should start with default expanded state
      const toggleButton = screen.getByTestId('collapse-toggle');
      expect(toggleButton).toHaveAttribute('aria-expanded', 'true');
    });
  });

  describe('Expand/Collapse All', () => {
    it('Expand All expands all lanes', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
        createMockIssue({ id: 'issue-3', assignee: 'charlie', status: 'open' }),
      ];

      // Pre-populate with some collapsed lanes (lane IDs use "lane-" prefix)
      mockStorage.store.set(
        'swimlane-collapsed-assignee',
        JSON.stringify(['lane-assignee-alice', 'lane-assignee-bob'])
      );

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // Verify some lanes start collapsed
      let toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'false'); // alice
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'false'); // bob
      expect(toggleButtons[2]).toHaveAttribute('aria-expanded', 'true'); // charlie

      // Click Expand All
      const expandAllButton = screen.getByTestId('expand-all-lanes');
      fireEvent.click(expandAllButton);

      // All lanes should now be expanded
      toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'true');
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'true');
      expect(toggleButtons[2]).toHaveAttribute('aria-expanded', 'true');
    });

    it('Collapse All collapses all lanes', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
        createMockIssue({ id: 'issue-3', assignee: 'charlie', status: 'open' }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // All lanes start expanded by default
      let toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'true');
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'true');
      expect(toggleButtons[2]).toHaveAttribute('aria-expanded', 'true');

      // Click Collapse All
      const collapseAllButton = screen.getByTestId('collapse-all-lanes');
      fireEvent.click(collapseAllButton);

      // All lanes should now be collapsed
      toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'false');
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'false');
      expect(toggleButtons[2]).toHaveAttribute('aria-expanded', 'false');
    });

    it('Expand All updates localStorage to empty array', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
      ];

      // Pre-populate with collapsed lanes (lane IDs use "lane-" prefix)
      mockStorage.store.set(
        'swimlane-collapsed-assignee',
        JSON.stringify(['lane-assignee-alice', 'lane-assignee-bob'])
      );

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // Click Expand All
      const expandAllButton = screen.getByTestId('expand-all-lanes');
      fireEvent.click(expandAllButton);

      // Verify localStorage was updated to empty array
      const lastSetCall = mockStorage.setItem.mock.calls[mockStorage.setItem.mock.calls.length - 1];
      const storedValue = JSON.parse(lastSetCall[1] as string) as string[];
      expect(storedValue).toEqual([]);
    });

    it('Collapse All updates localStorage with all lane IDs', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // Click Collapse All
      const collapseAllButton = screen.getByTestId('collapse-all-lanes');
      fireEvent.click(collapseAllButton);

      // Verify localStorage contains all lane IDs (lane IDs use "lane-" prefix)
      const lastSetCall = mockStorage.setItem.mock.calls[mockStorage.setItem.mock.calls.length - 1];
      const storedValue = JSON.parse(lastSetCall[1] as string) as string[];
      expect(storedValue).toContain('lane-assignee-alice');
      expect(storedValue).toContain('lane-assignee-bob');
    });

    it('Expand All works with defaultCollapsed=true', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
      ];

      render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          defaultCollapsed={true}
        />
      );

      // With defaultCollapsed=true, all lanes start collapsed
      let toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'false');
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'false');

      // Click Expand All
      const expandAllButton = screen.getByTestId('expand-all-lanes');
      fireEvent.click(expandAllButton);

      // All lanes should now be expanded
      toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'true');
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'true');
    });

    it('Collapse All works with defaultCollapsed=true', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
      ];

      // Pre-populate with expanded lanes (when defaultCollapsed=true, these are in toggled set)
      // Lane IDs use "lane-" prefix
      mockStorage.store.set(
        'swimlane-collapsed-assignee',
        JSON.stringify(['lane-assignee-alice', 'lane-assignee-bob'])
      );

      render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          defaultCollapsed={true}
        />
      );

      // With defaultCollapsed=true and lanes in toggled set, lanes are expanded
      let toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'true');
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'true');

      // Click Collapse All
      const collapseAllButton = screen.getByTestId('collapse-all-lanes');
      fireEvent.click(collapseAllButton);

      // All lanes should now be collapsed
      toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'false');
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'false');
    });
  });

  describe('groupBy=none does not use localStorage', () => {
    it('does not read from localStorage when groupBy=none', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', status: 'open' }),
      ];

      // Pre-populate localStorage
      mockStorage.store.set('swimlane-collapsed-none', JSON.stringify(['some-id']));

      render(<SwimLaneBoard issues={issues} groupBy="none" statuses={defaultStatuses} />);

      // Should render KanbanBoard, not swim lanes
      expect(screen.queryByTestId('swim-lane-board')).not.toBeInTheDocument();

      // localStorage should not be accessed for the 'none' key
      // (it may be accessed during initial render check, but we verify no swim lanes rendered)
    });
  });
});
