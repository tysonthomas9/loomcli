import { describe, it, expect } from 'vitest';
import { getOpenStatus } from '../issueCategory';
import fixture from '../../../testdata/blocker_parity_cases.json';

describe('issueCategory parity with Go taskfilter.go', () => {
  for (const c of fixture) {
    it(`case: ${c.id}`, () => {
      const issue = {
        design: c.issue.design || undefined,
        labels: c.issue.labels ?? undefined,
      };
      expect(getOpenStatus(issue)).toBe(c.expected.ts_open_status);
      const tsIsReady = getOpenStatus(issue) === 'ready';
      expect(tsIsReady).toBe(c.expected.go_ready_to_implement);
    });
  }
});
