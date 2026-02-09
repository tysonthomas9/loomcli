/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for formatIssueId utility.
 */

import { describe, it, expect, vi } from 'vitest';

import { formatIssueId } from '../formatIssueId';

describe('formatIssueId', () => {
  it('returns "unknown" for empty string', () => {
    expect(formatIssueId('')).toBe('unknown');
  });

  it('returns short ID as-is when length <= 10', () => {
    expect(formatIssueId('beads-v3vw')).toBe('beads-v3vw');
  });

  it('returns single character as-is', () => {
    expect(formatIssueId('a')).toBe('a');
  });

  it('returns exactly 10 char ID as-is', () => {
    expect(formatIssueId('1234567890')).toBe('1234567890');
  });

  it('returns last 7 characters for ID longer than 10', () => {
    expect(formatIssueId('beads-abcdefgh')).toBe('bcdefgh');
  });

  it('returns last 7 characters for very long ID', () => {
    expect(formatIssueId('some-very-long-issue-id-12345')).toBe('d-12345');
  });

  it('warns in development for empty ID', () => {
    const originalEnv = process.env.NODE_ENV;
    process.env.NODE_ENV = 'development';

    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

    formatIssueId('');

    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringContaining('empty issue ID')
    );

    warnSpy.mockRestore();
    process.env.NODE_ENV = originalEnv;
  });
});
