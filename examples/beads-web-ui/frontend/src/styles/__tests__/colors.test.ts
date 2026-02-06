/**
 * Unit tests for color constants and helper functions.
 *
 * These tests verify that color constants match the values defined in variables.css
 * and that helper functions return the correct colors.
 */

import { describe, it, expect } from 'vitest';

import {
  StateColors,
  StatusColors,
  PriorityColors,
  SemanticColors,
  TypeColors,
  getStateColor,
  getPriorityColor,
  getStatusColor,
} from '../colors';

describe('StateColors', () => {
  it('has correct color for blocked state', () => {
    expect(StateColors.blocked).toBe('#ef4444'); // red-500
  });

  it('has correct color for ready state', () => {
    expect(StateColors.ready).toBe('#22c55e'); // green-500
  });

  it('has correct color for closed state', () => {
    expect(StateColors.closed).toBe('#10b981'); // green-500 (same as status-closed)
  });

  it('contains exactly 3 state colors', () => {
    expect(Object.keys(StateColors)).toHaveLength(3);
  });
});

describe('StatusColors', () => {
  it('has correct color for open status', () => {
    expect(StatusColors.open).toBe('#3b82f6'); // blue-500
  });

  it('has correct color for in_progress status', () => {
    expect(StatusColors.in_progress).toBe('#f59e0b'); // amber-500
  });

  it('has correct color for closed status', () => {
    expect(StatusColors.closed).toBe('#10b981'); // green-500
  });

  it('has correct color for review status', () => {
    expect(StatusColors.review).toBe('#8b5cf6'); // purple-500
  });

  it('contains exactly 4 status colors', () => {
    expect(Object.keys(StatusColors)).toHaveLength(4);
  });
});

describe('PriorityColors', () => {
  it('has correct color for priority 0 (critical)', () => {
    expect(PriorityColors[0]).toBe('#dc2626'); // red-600
  });

  it('has correct color for priority 1 (high)', () => {
    expect(PriorityColors[1]).toBe('#ea580c'); // orange-600
  });

  it('has correct color for priority 2 (medium)', () => {
    expect(PriorityColors[2]).toBe('#ca8a04'); // yellow-600
  });

  it('has correct color for priority 3 (normal)', () => {
    expect(PriorityColors[3]).toBe('#2563eb'); // blue-600
  });

  it('has correct color for priority 4 (low/backlog)', () => {
    expect(PriorityColors[4]).toBe('#6b7280'); // gray-500
  });

  it('contains exactly 5 priority colors (0-4)', () => {
    expect(Object.keys(PriorityColors)).toHaveLength(5);
  });
});

describe('SemanticColors', () => {
  it('has correct primary color', () => {
    expect(SemanticColors.primary).toBe('#3b82f6'); // blue-500
  });

  it('has correct success color', () => {
    expect(SemanticColors.success).toBe('#22c55e'); // green-500
  });

  it('has correct warning color', () => {
    expect(SemanticColors.warning).toBe('#f59e0b'); // amber-500
  });

  it('has correct danger color', () => {
    expect(SemanticColors.danger).toBe('#ef4444'); // red-500
  });

  it('has correct info color', () => {
    expect(SemanticColors.info).toBe('#06b6d4'); // cyan-500
  });

  it('contains exactly 5 semantic colors', () => {
    expect(Object.keys(SemanticColors)).toHaveLength(5);
  });
});

describe('TypeColors', () => {
  it('has correct color for epic type', () => {
    expect(TypeColors.epic).toBe('#8b5cf6'); // purple-500
  });

  it('contains exactly 1 type color', () => {
    expect(Object.keys(TypeColors)).toHaveLength(1);
  });
});

describe('getStateColor', () => {
  it('returns correct color for blocked state', () => {
    expect(getStateColor('blocked')).toBe('#ef4444');
  });

  it('returns correct color for ready state', () => {
    expect(getStateColor('ready')).toBe('#22c55e');
  });

  it('returns correct color for closed state', () => {
    expect(getStateColor('closed')).toBe('#10b981');
  });

  it('returns same value as StateColors object', () => {
    expect(getStateColor('blocked')).toBe(StateColors.blocked);
    expect(getStateColor('ready')).toBe(StateColors.ready);
    expect(getStateColor('closed')).toBe(StateColors.closed);
  });
});

describe('getPriorityColor', () => {
  it('returns correct color for priority 0', () => {
    expect(getPriorityColor(0)).toBe('#dc2626');
  });

  it('returns correct color for priority 1', () => {
    expect(getPriorityColor(1)).toBe('#ea580c');
  });

  it('returns correct color for priority 2', () => {
    expect(getPriorityColor(2)).toBe('#ca8a04');
  });

  it('returns correct color for priority 3', () => {
    expect(getPriorityColor(3)).toBe('#2563eb');
  });

  it('returns correct color for priority 4', () => {
    expect(getPriorityColor(4)).toBe('#6b7280');
  });

  it('returns same value as PriorityColors object', () => {
    expect(getPriorityColor(0)).toBe(PriorityColors[0]);
    expect(getPriorityColor(1)).toBe(PriorityColors[1]);
    expect(getPriorityColor(2)).toBe(PriorityColors[2]);
    expect(getPriorityColor(3)).toBe(PriorityColors[3]);
    expect(getPriorityColor(4)).toBe(PriorityColors[4]);
  });
});

describe('getStatusColor', () => {
  it('returns correct color for open status', () => {
    expect(getStatusColor('open')).toBe('#3b82f6');
  });

  it('returns correct color for in_progress status', () => {
    expect(getStatusColor('in_progress')).toBe('#f59e0b');
  });

  it('returns correct color for closed status', () => {
    expect(getStatusColor('closed')).toBe('#10b981');
  });

  it('returns same value as StatusColors object', () => {
    expect(getStatusColor('open')).toBe(StatusColors.open);
    expect(getStatusColor('in_progress')).toBe(StatusColors.in_progress);
    expect(getStatusColor('closed')).toBe(StatusColors.closed);
  });
});

describe('Color consistency with variables.css', () => {
  // These tests document the expected CSS variable values
  // to ensure colors.ts stays in sync with variables.css

  it('state colors match CSS variables', () => {
    // --color-blocked: #ef4444
    expect(StateColors.blocked).toBe('#ef4444');
    // --color-ready: #22c55e
    expect(StateColors.ready).toBe('#22c55e');
    // --color-status-closed: #10b981
    expect(StateColors.closed).toBe('#10b981');
  });

  it('status colors match CSS variables', () => {
    // --color-status-open: #3b82f6
    expect(StatusColors.open).toBe('#3b82f6');
    // --color-status-in-progress: #f59e0b
    expect(StatusColors.in_progress).toBe('#f59e0b');
    // --color-status-closed: #10b981
    expect(StatusColors.closed).toBe('#10b981');
  });

  it('priority colors match CSS variables', () => {
    // --color-priority-0: #dc2626
    expect(PriorityColors[0]).toBe('#dc2626');
    // --color-priority-1: #ea580c
    expect(PriorityColors[1]).toBe('#ea580c');
    // --color-priority-2: #ca8a04
    expect(PriorityColors[2]).toBe('#ca8a04');
    // --color-priority-3: #2563eb
    expect(PriorityColors[3]).toBe('#2563eb');
    // --color-priority-4: #6b7280
    expect(PriorityColors[4]).toBe('#6b7280');
  });

  it('semantic colors match CSS variables', () => {
    // --color-primary: #3b82f6
    expect(SemanticColors.primary).toBe('#3b82f6');
    // --color-success: #22c55e
    expect(SemanticColors.success).toBe('#22c55e');
    // --color-warning: #f59e0b
    expect(SemanticColors.warning).toBe('#f59e0b');
    // --color-danger: #ef4444
    expect(SemanticColors.danger).toBe('#ef4444');
    // --color-info: #06b6d4
    expect(SemanticColors.info).toBe('#06b6d4');
  });

  it('type colors match CSS variables', () => {
    // --color-type-epic: #8b5cf6
    expect(TypeColors.epic).toBe('#8b5cf6');
  });
});
