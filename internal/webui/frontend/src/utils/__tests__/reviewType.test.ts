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
    it('returns "plan" when title contains [Need Review]', () => {
      const result = getReviewType({ title: '[Need Review] My feature plan' });

      expect(result).toBe('plan');
    });

    it('returns "plan" when [Need Review] is in the middle of title', () => {
      const result = getReviewType({ title: 'Feature [Need Review] for approval' });

      expect(result).toBe('plan');
    });

    it('returns "plan" when [Need Review] is at the end of title', () => {
      const result = getReviewType({ title: 'My feature plan [Need Review]' });

      expect(result).toBe('plan');
    });
  });

  describe('code review', () => {
    it('returns "code" when status is "review"', () => {
      const result = getReviewType({ title: 'Implement feature X', status: 'review' });

      expect(result).toBe('code');
    });

    it('returns "code" when status is "review" and title has no [Need Review]', () => {
      const result = getReviewType({ title: 'Regular code task', status: 'review' });

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
    it('plan takes priority over code review status', () => {
      const result = getReviewType({
        title: '[Need Review] Code review request',
        status: 'review',
      });

      expect(result).toBe('plan');
    });

    it('plan takes priority over blocked+notes', () => {
      const result = getReviewType({
        title: '[Need Review] Blocked item',
        status: 'blocked',
        notes: 'Some notes',
      });

      expect(result).toBe('plan');
    });
  });

  describe('edge cases', () => {
    it('[Need Review] detection is case sensitive', () => {
      const result = getReviewType({ title: '[need review] lowercase' });

      expect(result).toBeNull();
    });

    it('handles undefined title gracefully', () => {
      // @ts-expect-error Testing undefined title
      const result = getReviewType({ title: undefined });

      expect(result).toBeNull();
    });

    it('handles title with only [Need Review]', () => {
      const result = getReviewType({ title: '[Need Review]' });

      expect(result).toBe('plan');
    });
  });
});
