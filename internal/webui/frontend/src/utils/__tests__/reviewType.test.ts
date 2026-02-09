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
    it('returns "plan" when status is "review" and no PR URL in external_ref', () => {
      const result = getReviewType({ title: 'My feature plan', status: 'review' });

      expect(result).toBe('plan');
    });

    it('returns "plan" when status is "review" and external_ref is null', () => {
      const result = getReviewType({
        title: 'Feature plan',
        status: 'review',
        external_ref: null,
      });

      expect(result).toBe('plan');
    });

    it('returns "plan" when status is "review" and external_ref is empty string', () => {
      const result = getReviewType({
        title: 'Feature plan',
        status: 'review',
        external_ref: '',
      });

      expect(result).toBe('plan');
    });

    it('returns "plan" when status is "review" and external_ref is not a PR URL', () => {
      const result = getReviewType({
        title: 'Feature plan',
        status: 'review',
        external_ref: 'https://github.com/org/repo/issues/42',
      });

      expect(result).toBe('plan');
    });
  });

  describe('code review', () => {
    it('returns "code" when status is "review" and external_ref is a PR URL', () => {
      const result = getReviewType({
        title: 'Implement feature X',
        status: 'review',
        external_ref: 'https://github.com/org/repo/pull/42',
      });

      expect(result).toBe('code');
    });

    it('returns "code" when external_ref contains /pulls/ path', () => {
      const result = getReviewType({
        title: 'Code review task',
        status: 'review',
        external_ref: 'https://github.com/org/repo/pulls/42',
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
    it('code review takes priority over plan when PR URL present', () => {
      const result = getReviewType({
        title: 'Task with PR',
        status: 'review',
        external_ref: 'https://github.com/org/repo/pull/42',
      });

      expect(result).toBe('code');
    });

    it('review status takes priority over blocked+notes', () => {
      const result = getReviewType({
        title: 'Review item',
        status: 'review',
        notes: 'Some notes',
      });

      expect(result).toBe('plan');
    });
  });

  describe('edge cases', () => {
    it('handles undefined title gracefully', () => {
      // @ts-expect-error Testing undefined title
      const result = getReviewType({ title: undefined, status: 'review' });

      expect(result).toBe('plan');
    });

    it('handles external_ref with /pull/ in different positions', () => {
      const result = getReviewType({
        title: 'Task',
        status: 'review',
        external_ref: 'https://github.example.com/pull/123',
      });

      expect(result).toBe('code');
    });

    it('returns null for blocked without notes even with PR URL', () => {
      const result = getReviewType({
        title: 'Blocked task',
        status: 'blocked',
        external_ref: 'https://github.com/org/repo/pull/42',
      });

      expect(result).toBeNull();
    });
  });
});
