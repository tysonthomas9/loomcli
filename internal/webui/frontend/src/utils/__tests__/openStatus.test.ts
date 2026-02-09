/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for getOpenStatus utility.
 */

import { describe, it, expect } from 'vitest';

import { getOpenStatus } from '../openStatus';

describe('getOpenStatus', () => {
  describe('ready status', () => {
    it('returns "ready" when design has content', () => {
      const result = getOpenStatus({ design: 'some plan text' });

      expect(result).toBe('ready');
    });
  });

  describe('needs_plan status', () => {
    it('returns "needs_plan" when design is empty string', () => {
      const result = getOpenStatus({ design: '' });

      expect(result).toBe('needs_plan');
    });

    it('returns "needs_plan" when design is undefined', () => {
      const result = getOpenStatus({ design: undefined });

      expect(result).toBe('needs_plan');
    });

    it('returns "needs_plan" when no design field is present', () => {
      const result = getOpenStatus({});

      expect(result).toBe('needs_plan');
    });
  });
});
