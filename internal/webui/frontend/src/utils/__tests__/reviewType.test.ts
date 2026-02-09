/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for getReviewType utility.
 */

import { describe, it, expect } from 'vitest';

import { getReviewType } from '../reviewType';

describe('getReviewType', () => {
  describe('plan review', () => {
    it('returns "plan" when status is review with no external_ref', () => {
      const result = getReviewType({ title: 'Design auth flow', status: 'review' });

      expect(result).toBe('plan');
    });

    it('returns "plan" when status is review with non-PR external_ref', () => {
      const result = getReviewType({ title: 'Task', status: 'review', external_ref: 'JIRA-123' });

      expect(result).toBe('plan');
    });

    it('returns "plan" when status is review with null external_ref', () => {
      const result = getReviewType({ title: 'Task', status: 'review', external_ref: null });

      expect(result).toBe('plan');
    });

    it('returns "plan" when status is review with empty string external_ref', () => {
      const result = getReviewType({ title: 'Task', status: 'review', external_ref: '' });

      expect(result).toBe('plan');
    });
  });

  describe('code review', () => {
    it('returns "code" when status is review with PR URL in external_ref', () => {
      const result = getReviewType({
        title: 'Implement feature X',
        status: 'review',
        external_ref: 'https://github.com/owner/repo/pull/42',
      });

      expect(result).toBe('code');
    });

    it('returns "code" when external_ref contains /pulls/ path', () => {
      const result = getReviewType({
        title: 'Task',
        status: 'review',
        external_ref: 'https://github.com/owner/repo/pulls/123',
      });

      expect(result).toBe('code');
    });
  });

  describe('help review', () => {
    it('returns "help" when status is "blocked" with notes', () => {
      const result = getReviewType({
        title: 'Task needing help',
        status: 'blocked',
        notes: 'Stuck on database migration',
      });

      expect(result).toBe('help');
    });

    it('returns null when status is "blocked" without notes', () => {
      const result = getReviewType({
        title: 'Blocked task',
        status: 'blocked',
      });

      expect(result).toBeNull();
    });

    it('returns null when status is "blocked" with empty string notes', () => {
      const result = getReviewType({
        title: 'Blocked task',
        status: 'blocked',
        notes: '',
      });

      expect(result).toBeNull();
    });
  });

  describe('no review type', () => {
    it('returns null for regular issues', () => {
      const result = getReviewType({ title: 'Regular task', status: 'open' });

      expect(result).toBeNull();
    });

    it('returns null for in_progress status', () => {
      const result = getReviewType({ title: 'Working on it', status: 'in_progress' });

      expect(result).toBeNull();
    });

    it('returns null for closed status', () => {
      const result = getReviewType({ title: 'Done task', status: 'closed' });

      expect(result).toBeNull();
    });

    it('returns null when no status is provided', () => {
      const result = getReviewType({ title: 'No status task' });

      expect(result).toBeNull();
    });
  });

  describe('priority rules', () => {
    it('code takes priority when external_ref has PR URL even with notes', () => {
      const result = getReviewType({
        title: 'Task',
        status: 'review',
        notes: 'Some notes',
        external_ref: 'https://github.com/owner/repo/pull/1',
      });

      expect(result).toBe('code');
    });

    it('plan review when status is review without PR URL regardless of title', () => {
      const result = getReviewType({
        title: '[Need Review] Code review request',
        status: 'review',
      });

      expect(result).toBe('plan');
    });
  });

  describe('edge cases', () => {
    it('handles undefined title gracefully', () => {
      // @ts-expect-error Testing undefined title
      const result = getReviewType({ title: undefined });

      expect(result).toBeNull();
    });

    it('returns "plan" for review status even with [Need Review] in title (title no longer matters)', () => {
      const result = getReviewType({ title: '[Need Review]', status: 'review' });

      expect(result).toBe('plan');
    });

    it('does not detect review from title alone (no status)', () => {
      const result = getReviewType({ title: '[Need Review] My feature plan' });

      expect(result).toBeNull();
    });
  });
});
