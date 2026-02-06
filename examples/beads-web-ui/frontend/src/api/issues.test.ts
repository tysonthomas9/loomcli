import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import type { Issue, IssueDetails, Statistics, WorkFilter } from '@/types';

import { ApiError, get, post, patch } from './client';
import {
  getIssue,
  getReadyIssues,
  getStats,
  createIssue,
  updateIssue,
  closeIssue,
  fetchGraphIssues,
  buildQueryString,
  unwrap,
  mapWorkFilterToQueryParams,
} from './issues';
import type { CreateIssueRequest, UpdateIssueRequest } from './issues';

// Import mocked functions after mock setup

// Mock the client module
vi.mock('./client', () => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  ApiError: class ApiError extends Error {
    constructor(
      public status: number,
      public statusText: string,
      public body?: unknown
    ) {
      super(`API Error: ${status} ${statusText}`);
      this.name = 'ApiError';
    }
  },
}));

const mockGet = get as ReturnType<typeof vi.fn>;
const mockPost = post as ReturnType<typeof vi.fn>;
const mockPatch = patch as ReturnType<typeof vi.fn>;

describe('issues API', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // ============= Helper Function Tests =============

  describe('buildQueryString', () => {
    it('returns empty string for empty object', () => {
      expect(buildQueryString({})).toBe('');
    });

    it('returns empty string when all values are undefined or null', () => {
      expect(buildQueryString({ a: undefined, b: null })).toBe('');
    });

    it('builds query string from simple key-value pairs', () => {
      const result = buildQueryString({ status: 'open', priority: 'high' });
      expect(result).toBe('?status=open&priority=high');
    });

    it('converts numbers to strings', () => {
      const result = buildQueryString({ limit: 10, offset: 5 });
      expect(result).toBe('?limit=10&offset=5');
    });

    it('converts booleans to "true" or "false"', () => {
      expect(buildQueryString({ active: true })).toBe('?active=true');
      expect(buildQueryString({ active: false })).toBe('?active=false');
    });

    it('joins arrays with commas', () => {
      const result = buildQueryString({ labels: ['bug', 'urgent', 'frontend'] });
      expect(result).toBe('?labels=bug%2Curgent%2Cfrontend');
    });

    it('omits empty arrays', () => {
      const result = buildQueryString({ labels: [], status: 'open' });
      expect(result).toBe('?status=open');
    });

    it('handles mixed parameter types', () => {
      const result = buildQueryString({
        status: 'open',
        limit: 20,
        includeArchived: false,
        labels: ['a', 'b'],
        empty: undefined,
      });
      // URLSearchParams maintains insertion order
      expect(result).toContain('status=open');
      expect(result).toContain('limit=20');
      expect(result).toContain('includeArchived=false');
      expect(result).toContain('labels=a%2Cb');
      expect(result).not.toContain('empty');
    });
  });

  describe('unwrap', () => {
    it('returns data from successful response', () => {
      const successResponse = { success: true as const, data: { id: '1', title: 'Test' } };
      const result = unwrap(successResponse);
      expect(result).toEqual({ id: '1', title: 'Test' });
    });

    it('returns array data from successful response', () => {
      const items = [{ id: '1' }, { id: '2' }];
      const successResponse = { success: true as const, data: items };
      const result = unwrap(successResponse);
      expect(result).toEqual(items);
    });

    it('throws ApiError on failure response', () => {
      const failureResponse = {
        success: false as const,
        error: 'Something went wrong',
      };
      expect(() => unwrap(failureResponse)).toThrow(ApiError);
    });

    it('includes error message from failure response', () => {
      const failureResponse = {
        success: false as const,
        error: 'Issue not found',
      };
      try {
        unwrap(failureResponse);
        expect.fail('Expected unwrap to throw');
      } catch (e) {
        expect(e).toBeInstanceOf(ApiError);
        const apiError = e as ApiError;
        expect(apiError.status).toBe(0);
        expect(apiError.statusText).toBe('Issue not found');
      }
    });

    it('handles failure response with code', () => {
      const failureResponse = {
        success: false as const,
        error: 'Validation failed',
        code: 'VALIDATION_ERROR',
      };
      expect(() => unwrap(failureResponse)).toThrow(ApiError);
    });
  });

  describe('mapWorkFilterToQueryParams', () => {
    it('returns empty object for empty filter', () => {
      const result = mapWorkFilterToQueryParams({});
      expect(result).toEqual({});
    });

    it('renames sort_policy to sort', () => {
      const filter: WorkFilter = { sort_policy: 'priority' };
      const result = mapWorkFilterToQueryParams(filter);
      expect(result).toEqual({ sort: 'priority' });
      expect(result).not.toHaveProperty('sort_policy');
    });

    it('passes through other properties unchanged', () => {
      const filter: WorkFilter = {
        status: 'open',
        priority: 'high',
        labels: ['bug'],
      };
      const result = mapWorkFilterToQueryParams(filter);
      expect(result).toEqual({
        status: 'open',
        priority: 'high',
        labels: ['bug'],
      });
    });

    it('handles filter with all properties including sort_policy', () => {
      const filter: WorkFilter = {
        status: 'in_progress',
        priority: 'medium',
        labels: ['feature', 'v2'],
        sort_policy: 'oldest',
        assignee: 'user123',
      };
      const result = mapWorkFilterToQueryParams(filter);
      expect(result).toEqual({
        status: 'in_progress',
        priority: 'medium',
        labels: ['feature', 'v2'],
        sort: 'oldest',
        assignee: 'user123',
      });
      expect(result).not.toHaveProperty('sort_policy');
    });

    it('does not add sort if sort_policy is undefined', () => {
      const filter: WorkFilter = { status: 'open', sort_policy: undefined };
      const result = mapWorkFilterToQueryParams(filter);
      expect(result).not.toHaveProperty('sort');
      expect(result).not.toHaveProperty('sort_policy');
    });
  });

  // ============= Read Operation Tests =============

  describe('getIssue', () => {
    const mockIssueDetails: IssueDetails = {
      id: 'issue-123',
      title: 'Test Issue',
      description: 'A test issue',
      issue_type: 'bug',
      priority: 'high',
      status: 'open',
      labels: ['test'],
      created_at: '2024-01-01T00:00:00Z',
      updated_at: '2024-01-01T00:00:00Z',
      dependencies: [],
      dependents: [],
      blocked_by: [],
      children: [],
      comments: [],
    };

    it('calls get with correct URL', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssueDetails });

      await getIssue('issue-123');

      expect(mockGet).toHaveBeenCalledWith('/api/issues/issue-123');
    });

    it('unwraps successful response and returns IssueDetails', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssueDetails });

      const result = await getIssue('issue-123');

      expect(result).toEqual(mockIssueDetails);
    });

    it('encodes special characters in ID', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssueDetails });

      await getIssue('issue/with/slashes');

      expect(mockGet).toHaveBeenCalledWith('/api/issues/issue%2Fwith%2Fslashes');
    });

    it('encodes spaces in ID', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssueDetails });

      await getIssue('issue with spaces');

      expect(mockGet).toHaveBeenCalledWith('/api/issues/issue%20with%20spaces');
    });

    it('throws ApiError on failure response', async () => {
      mockGet.mockResolvedValue({ success: false, error: 'Issue not found' });

      await expect(getIssue('nonexistent')).rejects.toThrow(ApiError);
    });

    it('propagates ApiError from client', async () => {
      const error = new ApiError(404, 'Not Found', { error: 'Issue not found' });
      mockGet.mockRejectedValue(error);

      await expect(getIssue('nonexistent')).rejects.toThrow(ApiError);
      await expect(getIssue('nonexistent')).rejects.toMatchObject({
        status: 404,
        statusText: 'Not Found',
      });
    });
  });

  describe('getReadyIssues', () => {
    const mockIssues: Issue[] = [
      {
        id: 'issue-1',
        title: 'First Issue',
        issue_type: 'task',
        priority: 'high',
        status: 'open',
        labels: [],
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      },
      {
        id: 'issue-2',
        title: 'Second Issue',
        issue_type: 'bug',
        priority: 'medium',
        status: 'open',
        labels: ['urgent'],
        created_at: '2024-01-02T00:00:00Z',
        updated_at: '2024-01-02T00:00:00Z',
      },
    ];

    it('calls get with /api/ready when no options', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssues });

      await getReadyIssues();

      expect(mockGet).toHaveBeenCalledWith('/api/ready');
    });

    it('calls get with /api/ready when empty options', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssues });

      await getReadyIssues({});

      expect(mockGet).toHaveBeenCalledWith('/api/ready');
    });

    it('builds query string from filter options', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssues });

      await getReadyIssues({ status: 'open', priority: 'high' });

      expect(mockGet).toHaveBeenCalledWith('/api/ready?status=open&priority=high');
    });

    it('renames sort_policy to sort in query', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssues });

      await getReadyIssues({ sort_policy: 'priority' });

      expect(mockGet).toHaveBeenCalledWith('/api/ready?sort=priority');
    });

    it('unwraps successful response and returns Issue array', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssues });

      const result = await getReadyIssues();

      expect(result).toEqual(mockIssues);
    });

    it('throws ApiError on failure response', async () => {
      mockGet.mockResolvedValue({ success: false, error: 'Database unavailable' });

      await expect(getReadyIssues()).rejects.toThrow(ApiError);
    });

    it('propagates ApiError from client', async () => {
      const error = new ApiError(500, 'Internal Server Error');
      mockGet.mockRejectedValue(error);

      await expect(getReadyIssues()).rejects.toThrow(ApiError);
      await expect(getReadyIssues()).rejects.toMatchObject({
        status: 500,
      });
    });

    it('handles complex filter with labels array', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockIssues });

      await getReadyIssues({
        labels: ['bug', 'urgent'],
        sort_policy: 'oldest',
        assignee: 'dev1',
      });

      const callArg = mockGet.mock.calls[0][0] as string;
      expect(callArg).toContain('/api/ready?');
      expect(callArg).toContain('labels=bug%2Curgent');
      expect(callArg).toContain('sort=oldest');
      expect(callArg).toContain('assignee=dev1');
    });
  });

  describe('getStats', () => {
    const mockStats: Statistics = {
      total_issues: 100,
      open_issues: 45,
      closed_issues: 55,
      blocked_issues: 10,
      issues_by_type: {
        bug: 30,
        feature: 40,
        task: 20,
        chore: 10,
      },
      issues_by_priority: {
        high: 25,
        medium: 50,
        low: 25,
      },
    };

    it('calls get with /api/stats', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockStats });

      await getStats();

      expect(mockGet).toHaveBeenCalledWith('/api/stats');
    });

    it('unwraps successful response and returns Statistics', async () => {
      mockGet.mockResolvedValue({ success: true, data: mockStats });

      const result = await getStats();

      expect(result).toEqual(mockStats);
    });

    it('throws ApiError on failure response', async () => {
      mockGet.mockResolvedValue({ success: false, error: 'Stats unavailable' });

      await expect(getStats()).rejects.toThrow(ApiError);
    });

    it('propagates ApiError from client', async () => {
      const error = new ApiError(503, 'Service Unavailable');
      mockGet.mockRejectedValue(error);

      await expect(getStats()).rejects.toThrow(ApiError);
    });
  });

  // ============= Graph Operation Tests =============

  describe('fetchGraphIssues', () => {
    it('calls get with /api/issues/graph when no options', async () => {
      mockGet.mockResolvedValue({ success: true, issues: [] });

      await fetchGraphIssues();

      expect(mockGet).toHaveBeenCalledWith('/api/issues/graph');
    });

    it('calls get with /api/issues/graph when empty options', async () => {
      mockGet.mockResolvedValue({ success: true, issues: [] });

      await fetchGraphIssues({});

      expect(mockGet).toHaveBeenCalledWith('/api/issues/graph');
    });

    it('builds query string with status parameter', async () => {
      mockGet.mockResolvedValue({ success: true, issues: [] });

      await fetchGraphIssues({ status: 'open' });

      expect(mockGet).toHaveBeenCalledWith('/api/issues/graph?status=open');
    });

    it('builds query string with status=closed parameter', async () => {
      mockGet.mockResolvedValue({ success: true, issues: [] });

      await fetchGraphIssues({ status: 'closed' });

      expect(mockGet).toHaveBeenCalledWith('/api/issues/graph?status=closed');
    });

    it('builds query string with status=all parameter', async () => {
      mockGet.mockResolvedValue({ success: true, issues: [] });

      await fetchGraphIssues({ status: 'all' });

      expect(mockGet).toHaveBeenCalledWith('/api/issues/graph?status=all');
    });

    it('builds query string with include_closed=true parameter', async () => {
      mockGet.mockResolvedValue({ success: true, issues: [] });

      await fetchGraphIssues({ includeClosed: true });

      expect(mockGet).toHaveBeenCalledWith('/api/issues/graph?include_closed=true');
    });

    it('builds query string with include_closed=false parameter', async () => {
      mockGet.mockResolvedValue({ success: true, issues: [] });

      await fetchGraphIssues({ includeClosed: false });

      expect(mockGet).toHaveBeenCalledWith('/api/issues/graph?include_closed=false');
    });

    it('builds query string with both status and include_closed parameters', async () => {
      mockGet.mockResolvedValue({ success: true, issues: [] });

      await fetchGraphIssues({ status: 'all', includeClosed: true });

      const callArg = mockGet.mock.calls[0][0] as string;
      expect(callArg).toContain('/api/issues/graph?');
      expect(callArg).toContain('status=all');
      expect(callArg).toContain('include_closed=true');
    });

    it('returns empty array when issues is empty', async () => {
      mockGet.mockResolvedValue({ success: true, issues: [] });

      const result = await fetchGraphIssues();

      expect(result).toEqual([]);
    });

    it('returns empty array when issues is undefined in response', async () => {
      mockGet.mockResolvedValue({ success: true });

      const result = await fetchGraphIssues();

      expect(result).toEqual([]);
    });

    it('transforms simplified dependencies to full Dependency format', async () => {
      const graphApiResponse = {
        success: true,
        issues: [
          {
            id: 'issue-1',
            title: 'Issue with dependencies',
            issue_type: 'task',
            priority: 'high',
            status: 'open',
            labels: [],
            dependencies: [
              { depends_on_id: 'issue-2', type: 'blocks' },
              { depends_on_id: 'issue-3', type: 'related' },
            ],
          },
        ],
      };
      mockGet.mockResolvedValue(graphApiResponse);

      const result = await fetchGraphIssues();

      expect(result).toHaveLength(1);
      expect(result[0].dependencies).toHaveLength(2);
      expect(result[0].dependencies![0]).toEqual({
        issue_id: 'issue-1',
        depends_on_id: 'issue-2',
        type: 'blocks',
        created_at: '', // Not available in slim graph payload
      });
      expect(result[0].dependencies![1]).toEqual({
        issue_id: 'issue-1',
        depends_on_id: 'issue-3',
        type: 'related',
        created_at: '', // Not available in slim graph payload
      });
    });

    it('handles issues with no dependencies', async () => {
      const graphApiResponse = {
        success: true,
        issues: [
          {
            id: 'issue-1',
            title: 'Issue without dependencies',
            issue_type: 'bug',
            priority: 'medium',
            status: 'open',
            labels: ['test'],
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
          },
        ],
      };
      mockGet.mockResolvedValue(graphApiResponse);

      const result = await fetchGraphIssues();

      expect(result).toHaveLength(1);
      expect(result[0].id).toBe('issue-1');
      expect(result[0].dependencies).toBeUndefined();
    });

    it('handles issues with empty dependencies array', async () => {
      const graphApiResponse = {
        success: true,
        issues: [
          {
            id: 'issue-1',
            title: 'Issue with empty dependencies',
            issue_type: 'feature',
            priority: 'low',
            status: 'open',
            labels: [],
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
            dependencies: [],
          },
        ],
      };
      mockGet.mockResolvedValue(graphApiResponse);

      const result = await fetchGraphIssues();

      expect(result).toHaveLength(1);
      expect(result[0].dependencies).toEqual([]);
    });

    it('preserves slim issue fields during transformation', async () => {
      const graphApiResponse = {
        success: true,
        issues: [
          {
            id: 'issue-123',
            title: 'Full Issue',
            issue_type: 'task',
            priority: 2,
            status: 'in_progress',
            labels: ['urgent', 'frontend'],
            due_at: '2024-02-01T00:00:00Z',
            defer_until: '2024-01-15T00:00:00Z',
            dependencies: [{ depends_on_id: 'issue-456', type: 'blocks' }],
          },
        ],
      };
      mockGet.mockResolvedValue(graphApiResponse);

      const result = await fetchGraphIssues();

      expect(result).toHaveLength(1);
      const issue = result[0];
      // Slim payload fields
      expect(issue.id).toBe('issue-123');
      expect(issue.title).toBe('Full Issue');
      expect(issue.issue_type).toBe('task');
      expect(issue.priority).toBe(2);
      expect(issue.status).toBe('in_progress');
      expect(issue.labels).toEqual(['urgent', 'frontend']);
      expect(issue.due_at).toBe('2024-02-01T00:00:00Z');
      expect(issue.defer_until).toBe('2024-01-15T00:00:00Z');
      // Fields not in slim payload default to empty
      expect(issue.created_at).toBe('');
      expect(issue.updated_at).toBe('');
    });

    it('handles multiple issues with mixed dependency states', async () => {
      const graphApiResponse = {
        success: true,
        issues: [
          {
            id: 'issue-1',
            title: 'Issue 1',
            issue_type: 'task',
            priority: 'high',
            status: 'open',
            labels: [],
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
            dependencies: [{ depends_on_id: 'issue-2', type: 'blocks' }],
          },
          {
            id: 'issue-2',
            title: 'Issue 2',
            issue_type: 'bug',
            priority: 'medium',
            status: 'open',
            labels: [],
            created_at: '2024-01-02T00:00:00Z',
            updated_at: '2024-01-02T00:00:00Z',
          },
          {
            id: 'issue-3',
            title: 'Issue 3',
            issue_type: 'feature',
            priority: 'low',
            status: 'closed',
            labels: [],
            created_at: '2024-01-03T00:00:00Z',
            updated_at: '2024-01-03T00:00:00Z',
            dependencies: [],
          },
        ],
      };
      mockGet.mockResolvedValue(graphApiResponse);

      const result = await fetchGraphIssues();

      expect(result).toHaveLength(3);
      expect(result[0].dependencies).toHaveLength(1);
      expect(result[1].dependencies).toBeUndefined();
      expect(result[2].dependencies).toEqual([]);
    });

    it('throws ApiError on failure response', async () => {
      mockGet.mockResolvedValue({ success: false, error: 'Database unavailable' });

      await expect(fetchGraphIssues()).rejects.toThrow(ApiError);
    });

    it('throws ApiError with error message from failure response', async () => {
      mockGet.mockResolvedValue({ success: false, error: 'Graph query failed' });

      try {
        await fetchGraphIssues();
        expect.fail('Expected fetchGraphIssues to throw');
      } catch (e) {
        expect(e).toBeInstanceOf(ApiError);
        const apiError = e as ApiError;
        expect(apiError.statusText).toBe('Graph query failed');
      }
    });

    it('throws ApiError with "Unknown error" when error message is missing', async () => {
      mockGet.mockResolvedValue({ success: false });

      try {
        await fetchGraphIssues();
        expect.fail('Expected fetchGraphIssues to throw');
      } catch (e) {
        expect(e).toBeInstanceOf(ApiError);
        const apiError = e as ApiError;
        expect(apiError.statusText).toBe('Unknown error');
      }
    });

    it('propagates ApiError from client', async () => {
      const error = new ApiError(500, 'Internal Server Error');
      mockGet.mockRejectedValue(error);

      await expect(fetchGraphIssues()).rejects.toThrow(ApiError);
      await expect(fetchGraphIssues()).rejects.toMatchObject({
        status: 500,
      });
    });

    it('handles custom dependency types', async () => {
      const graphApiResponse = {
        success: true,
        issues: [
          {
            id: 'issue-1',
            title: 'Issue with custom dependency',
            issue_type: 'task',
            priority: 'high',
            status: 'open',
            labels: [],
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
            dependencies: [{ depends_on_id: 'issue-2', type: 'custom-type' }],
          },
        ],
      };
      mockGet.mockResolvedValue(graphApiResponse);

      const result = await fetchGraphIssues();

      expect(result[0].dependencies![0].type).toBe('custom-type');
    });
  });

  // ============= Write Operation Tests =============

  describe('createIssue', () => {
    const mockCreatedIssue: Issue = {
      id: 'new-issue-123',
      title: 'New Issue',
      issue_type: 'feature',
      priority: 'medium',
      status: 'open',
      labels: [],
      created_at: '2024-01-15T00:00:00Z',
      updated_at: '2024-01-15T00:00:00Z',
    };

    const validCreateRequest: CreateIssueRequest = {
      title: 'New Issue',
      issue_type: 'feature',
      priority: 'medium',
    };

    it('calls post with /api/issues and request body', async () => {
      mockPost.mockResolvedValue({ success: true, data: mockCreatedIssue });

      await createIssue(validCreateRequest);

      expect(mockPost).toHaveBeenCalledWith('/api/issues', validCreateRequest);
    });

    it('unwraps successful response and returns Issue', async () => {
      mockPost.mockResolvedValue({ success: true, data: mockCreatedIssue });

      const result = await createIssue(validCreateRequest);

      expect(result).toEqual(mockCreatedIssue);
    });

    it('handles create request with all optional fields', async () => {
      const fullRequest: CreateIssueRequest = {
        title: 'Full Issue',
        issue_type: 'bug',
        priority: 'high',
        id: 'custom-id',
        parent: 'parent-123',
        description: 'Detailed description',
        design: 'Design notes',
        acceptance_criteria: 'Must pass tests',
        notes: 'Additional notes',
        assignee: 'dev1',
        owner: 'pm1',
        created_by: 'user1',
        external_ref: 'JIRA-123',
        estimated_minutes: 120,
        labels: ['urgent', 'frontend'],
        dependencies: ['dep-1', 'dep-2'],
        due_at: '2024-02-01T00:00:00Z',
        defer_until: '2024-01-20T00:00:00Z',
      };

      mockPost.mockResolvedValue({ success: true, data: mockCreatedIssue });

      await createIssue(fullRequest);

      expect(mockPost).toHaveBeenCalledWith('/api/issues', fullRequest);
    });

    it('throws ApiError on failure response', async () => {
      mockPost.mockResolvedValue({
        success: false,
        error: 'Validation failed',
        code: 'VALIDATION_ERROR',
      });

      await expect(createIssue(validCreateRequest)).rejects.toThrow(ApiError);
    });

    it('propagates ApiError from client', async () => {
      const error = new ApiError(400, 'Bad Request', { error: 'Invalid issue type' });
      mockPost.mockRejectedValue(error);

      await expect(createIssue(validCreateRequest)).rejects.toThrow(ApiError);
      await expect(createIssue(validCreateRequest)).rejects.toMatchObject({
        status: 400,
      });
    });
  });

  describe('updateIssue', () => {
    const mockUpdatedIssue: Issue = {
      id: 'issue-123',
      title: 'Updated Title',
      issue_type: 'bug',
      priority: 'high',
      status: 'in_progress',
      labels: ['updated'],
      created_at: '2024-01-01T00:00:00Z',
      updated_at: '2024-01-15T00:00:00Z',
    };

    it('calls patch with correct URL and request body', async () => {
      const updateData: UpdateIssueRequest = { title: 'Updated Title' };
      mockPatch.mockResolvedValue({ success: true, data: mockUpdatedIssue });

      await updateIssue('issue-123', updateData);

      expect(mockPatch).toHaveBeenCalledWith('/api/issues/issue-123', updateData);
    });

    it('encodes special characters in ID', async () => {
      const updateData: UpdateIssueRequest = { status: 'closed' };
      mockPatch.mockResolvedValue({ success: true, data: mockUpdatedIssue });

      await updateIssue('issue/special', updateData);

      expect(mockPatch).toHaveBeenCalledWith('/api/issues/issue%2Fspecial', updateData);
    });

    it('unwraps successful response and returns Issue', async () => {
      const updateData: UpdateIssueRequest = { priority: 'high' };
      mockPatch.mockResolvedValue({ success: true, data: mockUpdatedIssue });

      const result = await updateIssue('issue-123', updateData);

      expect(result).toEqual(mockUpdatedIssue);
    });

    it('handles update request with all fields', async () => {
      const fullUpdate: UpdateIssueRequest = {
        title: 'New Title',
        description: 'New description',
        design: 'New design',
        notes: 'New notes',
        priority: 'low',
        status: 'blocked',
        assignee: 'new-assignee',
        labels: ['label1', 'label2'],
      };
      mockPatch.mockResolvedValue({ success: true, data: mockUpdatedIssue });

      await updateIssue('issue-123', fullUpdate);

      expect(mockPatch).toHaveBeenCalledWith('/api/issues/issue-123', fullUpdate);
    });

    it('throws ApiError on failure response', async () => {
      mockPatch.mockResolvedValue({ success: false, error: 'Issue not found' });

      await expect(updateIssue('nonexistent', { title: 'x' })).rejects.toThrow(ApiError);
    });

    it('propagates ApiError from client', async () => {
      const error = new ApiError(404, 'Not Found');
      mockPatch.mockRejectedValue(error);

      await expect(updateIssue('issue-123', { title: 'x' })).rejects.toThrow(ApiError);
    });
  });

  describe('closeIssue', () => {
    it('calls post with correct URL and empty body when no reason', async () => {
      mockPost.mockResolvedValue({ success: true, data: null });

      await closeIssue('issue-123');

      expect(mockPost).toHaveBeenCalledWith('/api/issues/issue-123/close', {});
    });

    it('calls post with reason in body when provided', async () => {
      mockPost.mockResolvedValue({ success: true, data: null });

      await closeIssue('issue-123', 'Completed successfully');

      expect(mockPost).toHaveBeenCalledWith('/api/issues/issue-123/close', {
        reason: 'Completed successfully',
      });
    });

    it('encodes special characters in ID', async () => {
      mockPost.mockResolvedValue({ success: true, data: null });

      await closeIssue('issue/with/path');

      expect(mockPost).toHaveBeenCalledWith('/api/issues/issue%2Fwith%2Fpath/close', {});
    });

    it('returns void on success', async () => {
      mockPost.mockResolvedValue({ success: true, data: null });

      const result = await closeIssue('issue-123');

      expect(result).toBeUndefined();
    });

    it('propagates ApiError from client', async () => {
      const error = new ApiError(403, 'Forbidden', { error: 'Cannot close issue' });
      mockPost.mockRejectedValue(error);

      await expect(closeIssue('issue-123')).rejects.toThrow(ApiError);
      await expect(closeIssue('issue-123')).rejects.toMatchObject({
        status: 403,
      });
    });

    it('throws ApiError on failure response', async () => {
      mockPost.mockResolvedValue({ success: false, error: 'Cannot close blocked issue' });

      await expect(closeIssue('issue-123')).rejects.toThrow(ApiError);
    });

    it('handles empty string reason as no reason', async () => {
      mockPost.mockResolvedValue({ success: true, data: null });

      // Empty string is falsy, so should send empty object
      await closeIssue('issue-123', '');

      expect(mockPost).toHaveBeenCalledWith('/api/issues/issue-123/close', {});
    });
  });

  // ============= Integration-style Tests =============

  describe('error handling consistency', () => {
    it('all read operations throw ApiError on network failure', async () => {
      const networkError = new ApiError(0, 'Network error');

      mockGet.mockRejectedValue(networkError);

      await expect(getIssue('123')).rejects.toThrow(ApiError);
      await expect(getReadyIssues()).rejects.toThrow(ApiError);
      await expect(getStats()).rejects.toThrow(ApiError);
      await expect(fetchGraphIssues()).rejects.toThrow(ApiError);
    });

    it('all write operations throw ApiError on network failure', async () => {
      const networkError = new ApiError(0, 'Network error');

      mockPost.mockRejectedValue(networkError);
      mockPatch.mockRejectedValue(networkError);

      await expect(
        createIssue({ title: 'x', issue_type: 'bug', priority: 'high' })
      ).rejects.toThrow(ApiError);
      await expect(updateIssue('123', { title: 'x' })).rejects.toThrow(ApiError);
      await expect(closeIssue('123')).rejects.toThrow(ApiError);
    });
  });
});
