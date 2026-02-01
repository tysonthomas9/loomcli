/**
 * Unit tests for Chrome visual testing helpers.
 *
 * These tests verify that TEST_SELECTORS are valid CSS selectors,
 * CONNECTION_STATES have required properties, and type exports work correctly.
 */

import { describe, it, expect } from 'vitest';

import {
  TEST_SELECTORS,
  CONNECTION_STATES,
  ISSUE_STATUSES,
  PRIORITY_LEVELS,
  TIMING_EXPECTATIONS,
  NETWORK_PATTERNS,
  A11Y_ATTRIBUTES,
} from '../chrome-visual-helpers';

import type {
  ConnectionStateKey,
  IssueStatus,
  PriorityLevel,
} from '../chrome-visual-helpers';

/**
 * Regex patterns to validate CSS selector string format.
 * These patterns verify the selectors follow valid CSS attribute selector syntax.
 */
const CSS_SELECTOR_PATTERNS = {
  // Attribute selector: [attr] or [attr="value"]
  attributeSelector: /^\[[\w-]+(?:="[^"]*")?\]$/,
  // Element with attribute: element[attr] or element[attr="value"]
  elementWithAttribute: /^[\w]+\[[\w-]+(?:="[^"]*")?\]$/,
  // Pseudo selector with element: element:pseudo or element:not(...)
  elementWithPseudo: /^[\w]+(?:\[[\w-]+(?:="[^"]*")?\])?(?::[\w-]+(?:\([^)]*\))?)+$/,
};

/**
 * Helper to validate CSS selector string format.
 * Checks if the selector follows valid CSS attribute selector patterns.
 */
function isValidCssSelectorFormat(selector: string): boolean {
  return (
    CSS_SELECTOR_PATTERNS.attributeSelector.test(selector) ||
    CSS_SELECTOR_PATTERNS.elementWithAttribute.test(selector) ||
    CSS_SELECTOR_PATTERNS.elementWithPseudo.test(selector)
  );
}

describe('TEST_SELECTORS', () => {
  describe('static selectors follow valid CSS format', () => {
    it('connectionStatus is a valid CSS attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.connectionStatus)).toBe(
        true
      );
    });

    it('connectedState is a valid CSS attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.connectedState)).toBe(
        true
      );
    });

    it('connectingState is a valid CSS attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.connectingState)).toBe(
        true
      );
    });

    it('reconnectingState is a valid CSS attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.reconnectingState)).toBe(
        true
      );
    });

    it('disconnectedState is a valid CSS attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.disconnectedState)).toBe(
        true
      );
    });

    it('retryButton is a valid CSS attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.retryButton)).toBe(true);
    });

    it('issueCard is a valid CSS element with attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.issueCard)).toBe(true);
    });

    it('blockedCard is a valid CSS element with attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.blockedCard)).toBe(true);
    });

    it('pendingCard is a valid CSS element with attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.pendingCard)).toBe(true);
    });

    it('statusColumn is a valid CSS element with attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.statusColumn)).toBe(true);
    });

    it('columnWithItems is a valid CSS element with attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.columnWithItems)).toBe(
        true
      );
    });

    it('emptyColumn is a valid CSS element with pseudo selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.emptyColumn)).toBe(true);
    });

    it('droppableArea is a valid CSS attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.droppableArea)).toBe(true);
    });

    it('activeDropTarget is a valid CSS attribute selector', () => {
      expect(isValidCssSelectorFormat(TEST_SELECTORS.activeDropTarget)).toBe(
        true
      );
    });
  });

  describe('dynamic selector functions return valid CSS format', () => {
    it('issueCardByPriority returns valid CSS element with attribute selectors', () => {
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.issueCardByPriority(0))
      ).toBe(true);
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.issueCardByPriority(1))
      ).toBe(true);
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.issueCardByPriority(2))
      ).toBe(true);
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.issueCardByPriority(3))
      ).toBe(true);
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.issueCardByPriority(4))
      ).toBe(true);
    });

    it('columnByStatus returns valid CSS element with attribute selectors', () => {
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.columnByStatus('open'))
      ).toBe(true);
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.columnByStatus('in_progress'))
      ).toBe(true);
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.columnByStatus('review'))
      ).toBe(true);
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.columnByStatus('blocked'))
      ).toBe(true);
      expect(
        isValidCssSelectorFormat(TEST_SELECTORS.columnByStatus('closed'))
      ).toBe(true);
    });
  });

  describe('selector values match expected patterns', () => {
    it('connection state selectors use data-state attribute', () => {
      expect(TEST_SELECTORS.connectionStatus).toBe('[data-state]');
      expect(TEST_SELECTORS.connectedState).toBe('[data-state="connected"]');
      expect(TEST_SELECTORS.connectingState).toBe('[data-state="connecting"]');
      expect(TEST_SELECTORS.reconnectingState).toBe(
        '[data-state="reconnecting"]'
      );
      expect(TEST_SELECTORS.disconnectedState).toBe(
        '[data-state="disconnected"]'
      );
    });

    it('issueCard selectors use data-priority attribute', () => {
      expect(TEST_SELECTORS.issueCard).toBe('article[data-priority]');
      expect(TEST_SELECTORS.issueCardByPriority(0)).toBe(
        'article[data-priority="0"]'
      );
      expect(TEST_SELECTORS.issueCardByPriority(2)).toBe(
        'article[data-priority="2"]'
      );
    });

    it('statusColumn selectors use data-status attribute', () => {
      expect(TEST_SELECTORS.statusColumn).toBe('section[data-status]');
      expect(TEST_SELECTORS.columnByStatus('open')).toBe(
        'section[data-status="open"]'
      );
      expect(TEST_SELECTORS.columnByStatus('closed')).toBe(
        'section[data-status="closed"]'
      );
    });

    it('retryButton uses aria-label attribute', () => {
      expect(TEST_SELECTORS.retryButton).toBe(
        '[aria-label="Retry connection now"]'
      );
    });
  });
});

describe('CONNECTION_STATES', () => {
  describe('all states have required properties', () => {
    const requiredProperties = [
      'dataState',
      'indicatorColor',
      'showsRetry',
      'hasAnimation',
    ] as const;

    it('connected state has all required properties', () => {
      for (const prop of requiredProperties) {
        expect(CONNECTION_STATES.connected).toHaveProperty(prop);
      }
      expect(CONNECTION_STATES.connected.text).toBeDefined();
    });

    it('connecting state has all required properties', () => {
      for (const prop of requiredProperties) {
        expect(CONNECTION_STATES.connecting).toHaveProperty(prop);
      }
      expect(CONNECTION_STATES.connecting.text).toBeDefined();
    });

    it('reconnecting state has all required properties', () => {
      for (const prop of requiredProperties) {
        expect(CONNECTION_STATES.reconnecting).toHaveProperty(prop);
      }
      // reconnecting uses textPattern instead of text
      expect(CONNECTION_STATES.reconnecting.textPattern).toBeDefined();
    });

    it('disconnected state has all required properties', () => {
      for (const prop of requiredProperties) {
        expect(CONNECTION_STATES.disconnected).toHaveProperty(prop);
      }
      expect(CONNECTION_STATES.disconnected.text).toBeDefined();
    });
  });

  describe('dataState values match selector state values', () => {
    it('connected dataState matches selector', () => {
      expect(CONNECTION_STATES.connected.dataState).toBe('connected');
      expect(TEST_SELECTORS.connectedState).toContain('connected');
    });

    it('connecting dataState matches selector', () => {
      expect(CONNECTION_STATES.connecting.dataState).toBe('connecting');
      expect(TEST_SELECTORS.connectingState).toContain('connecting');
    });

    it('reconnecting dataState matches selector', () => {
      expect(CONNECTION_STATES.reconnecting.dataState).toBe('reconnecting');
      expect(TEST_SELECTORS.reconnectingState).toContain('reconnecting');
    });

    it('disconnected dataState matches selector', () => {
      expect(CONNECTION_STATES.disconnected.dataState).toBe('disconnected');
      expect(TEST_SELECTORS.disconnectedState).toContain('disconnected');
    });
  });

  describe('showsRetry and hasAnimation are boolean', () => {
    it('connected has correct boolean values', () => {
      expect(typeof CONNECTION_STATES.connected.showsRetry).toBe('boolean');
      expect(typeof CONNECTION_STATES.connected.hasAnimation).toBe('boolean');
      expect(CONNECTION_STATES.connected.showsRetry).toBe(false);
      expect(CONNECTION_STATES.connected.hasAnimation).toBe(false);
    });

    it('connecting has correct boolean values', () => {
      expect(typeof CONNECTION_STATES.connecting.showsRetry).toBe('boolean');
      expect(typeof CONNECTION_STATES.connecting.hasAnimation).toBe('boolean');
      expect(CONNECTION_STATES.connecting.showsRetry).toBe(false);
      expect(CONNECTION_STATES.connecting.hasAnimation).toBe(true);
    });

    it('reconnecting has correct boolean values', () => {
      expect(typeof CONNECTION_STATES.reconnecting.showsRetry).toBe('boolean');
      expect(typeof CONNECTION_STATES.reconnecting.hasAnimation).toBe(
        'boolean'
      );
      expect(CONNECTION_STATES.reconnecting.showsRetry).toBe(true);
      expect(CONNECTION_STATES.reconnecting.hasAnimation).toBe(true);
    });

    it('disconnected has correct boolean values', () => {
      expect(typeof CONNECTION_STATES.disconnected.showsRetry).toBe('boolean');
      expect(typeof CONNECTION_STATES.disconnected.hasAnimation).toBe(
        'boolean'
      );
      expect(CONNECTION_STATES.disconnected.showsRetry).toBe(false);
      expect(CONNECTION_STATES.disconnected.hasAnimation).toBe(false);
    });
  });

  describe('text properties have correct format', () => {
    it('connected has static text', () => {
      expect(CONNECTION_STATES.connected.text).toBe('Connected');
    });

    it('connecting has static text with ellipsis', () => {
      expect(CONNECTION_STATES.connecting.text).toBe('Connecting...');
    });

    it('reconnecting has regex pattern for dynamic text', () => {
      expect(CONNECTION_STATES.reconnecting.textPattern).toBeInstanceOf(RegExp);
      expect(
        CONNECTION_STATES.reconnecting.textPattern.test(
          'Reconnecting (attempt 1)...'
        )
      ).toBe(true);
      expect(
        CONNECTION_STATES.reconnecting.textPattern.test(
          'Reconnecting (attempt 5)...'
        )
      ).toBe(true);
    });

    it('disconnected has static text', () => {
      expect(CONNECTION_STATES.disconnected.text).toBe('Disconnected');
    });
  });

  it('contains exactly 4 connection states', () => {
    expect(Object.keys(CONNECTION_STATES)).toHaveLength(4);
    expect(Object.keys(CONNECTION_STATES)).toEqual([
      'connected',
      'connecting',
      'reconnecting',
      'disconnected',
    ]);
  });
});

describe('ISSUE_STATUSES', () => {
  it('contains all expected statuses', () => {
    expect(ISSUE_STATUSES).toEqual([
      'open',
      'in_progress',
      'review',
      'blocked',
      'closed',
    ]);
  });

  it('has exactly 5 statuses', () => {
    expect(ISSUE_STATUSES.length).toBe(5);
  });

  it('statuses are non-empty strings', () => {
    for (const status of ISSUE_STATUSES) {
      expect(typeof status).toBe('string');
      expect(status.length).toBeGreaterThan(0);
    }
  });
});

describe('PRIORITY_LEVELS', () => {
  describe('all priority levels have required properties', () => {
    const requiredProperties = ['label', 'className', 'color'] as const;

    it('priority 0 has all required properties', () => {
      for (const prop of requiredProperties) {
        expect(PRIORITY_LEVELS[0]).toHaveProperty(prop);
      }
    });

    it('priority 1 has all required properties', () => {
      for (const prop of requiredProperties) {
        expect(PRIORITY_LEVELS[1]).toHaveProperty(prop);
      }
    });

    it('priority 2 has all required properties', () => {
      for (const prop of requiredProperties) {
        expect(PRIORITY_LEVELS[2]).toHaveProperty(prop);
      }
    });

    it('priority 3 has all required properties', () => {
      for (const prop of requiredProperties) {
        expect(PRIORITY_LEVELS[3]).toHaveProperty(prop);
      }
    });

    it('priority 4 has all required properties', () => {
      for (const prop of requiredProperties) {
        expect(PRIORITY_LEVELS[4]).toHaveProperty(prop);
      }
    });
  });

  describe('labels follow P# format', () => {
    it('priority labels are P0 through P4', () => {
      expect(PRIORITY_LEVELS[0].label).toBe('P0');
      expect(PRIORITY_LEVELS[1].label).toBe('P1');
      expect(PRIORITY_LEVELS[2].label).toBe('P2');
      expect(PRIORITY_LEVELS[3].label).toBe('P3');
      expect(PRIORITY_LEVELS[4].label).toBe('P4');
    });
  });

  describe('classNames follow priority# format', () => {
    it('priority classNames are priority0 through priority4', () => {
      expect(PRIORITY_LEVELS[0].className).toBe('priority0');
      expect(PRIORITY_LEVELS[1].className).toBe('priority1');
      expect(PRIORITY_LEVELS[2].className).toBe('priority2');
      expect(PRIORITY_LEVELS[3].className).toBe('priority3');
      expect(PRIORITY_LEVELS[4].className).toBe('priority4');
    });
  });

  it('contains exactly 5 priority levels (0-4)', () => {
    expect(Object.keys(PRIORITY_LEVELS)).toHaveLength(5);
  });
});

describe('TIMING_EXPECTATIONS', () => {
  it('has all required timing properties', () => {
    expect(TIMING_EXPECTATIONS).toHaveProperty('updateLatency');
    expect(TIMING_EXPECTATIONS).toHaveProperty('browserReconnectDelayMs');
    expect(TIMING_EXPECTATIONS).toHaveProperty('reconnectDelayMs');
    expect(TIMING_EXPECTATIONS).toHaveProperty('maxReconnectDelayMs');
  });

  it('all timing values are positive numbers', () => {
    expect(typeof TIMING_EXPECTATIONS.updateLatency).toBe('number');
    expect(TIMING_EXPECTATIONS.updateLatency).toBeGreaterThan(0);

    expect(typeof TIMING_EXPECTATIONS.browserReconnectDelayMs).toBe('number');
    expect(TIMING_EXPECTATIONS.browserReconnectDelayMs).toBeGreaterThan(0);

    expect(typeof TIMING_EXPECTATIONS.reconnectDelayMs).toBe('number');
    expect(TIMING_EXPECTATIONS.reconnectDelayMs).toBeGreaterThan(0);

    expect(typeof TIMING_EXPECTATIONS.maxReconnectDelayMs).toBe('number');
    expect(TIMING_EXPECTATIONS.maxReconnectDelayMs).toBeGreaterThan(0);
  });

  it('maxReconnectDelayMs is greater than reconnectDelayMs', () => {
    expect(TIMING_EXPECTATIONS.maxReconnectDelayMs).toBeGreaterThan(
      TIMING_EXPECTATIONS.reconnectDelayMs
    );
  });
});

describe('NETWORK_PATTERNS', () => {
  it('has all required endpoint patterns', () => {
    expect(NETWORK_PATTERNS).toHaveProperty('sseEndpoint');
    expect(NETWORK_PATTERNS).toHaveProperty('readyEndpoint');
    expect(NETWORK_PATTERNS).toHaveProperty('issueEndpoint');
    expect(NETWORK_PATTERNS).toHaveProperty('sseNetworkType');
  });

  it('sseEndpoint is a valid path string', () => {
    expect(typeof NETWORK_PATTERNS.sseEndpoint).toBe('string');
    expect(NETWORK_PATTERNS.sseEndpoint).toBe('/api/events');
  });

  it('readyEndpoint is a valid path string', () => {
    expect(typeof NETWORK_PATTERNS.readyEndpoint).toBe('string');
    expect(NETWORK_PATTERNS.readyEndpoint).toBe('/api/ready');
  });

  it('issueEndpoint is a valid RegExp', () => {
    expect(NETWORK_PATTERNS.issueEndpoint).toBeInstanceOf(RegExp);
    expect(NETWORK_PATTERNS.issueEndpoint.test('/api/issues/bd-123')).toBe(
      true
    );
    expect(NETWORK_PATTERNS.issueEndpoint.test('/api/issues/bd-abcd')).toBe(
      true
    );
    expect(NETWORK_PATTERNS.issueEndpoint.test('/api/issues/')).toBe(false);
    expect(NETWORK_PATTERNS.issueEndpoint.test('/api/issues')).toBe(false);
  });

  it('sseNetworkType describes Chrome DevTools type', () => {
    expect(typeof NETWORK_PATTERNS.sseNetworkType).toBe('string');
    expect(NETWORK_PATTERNS.sseNetworkType).toBe('eventsource');
  });
});

describe('A11Y_ATTRIBUTES', () => {
  it('has all required attribute groups', () => {
    expect(A11Y_ATTRIBUTES).toHaveProperty('connectionStatus');
    expect(A11Y_ATTRIBUTES).toHaveProperty('issueCard');
    expect(A11Y_ATTRIBUTES).toHaveProperty('statusColumn');
    expect(A11Y_ATTRIBUTES).toHaveProperty('retryButton');
  });

  describe('connectionStatus attributes', () => {
    it('has correct role', () => {
      expect(A11Y_ATTRIBUTES.connectionStatus.role).toBe('status');
    });

    it('has correct ariaLive', () => {
      expect(A11Y_ATTRIBUTES.connectionStatus.ariaLive).toBe('polite');
    });

    it('has ariaLabelPattern as RegExp', () => {
      expect(A11Y_ATTRIBUTES.connectionStatus.ariaLabelPattern).toBeInstanceOf(
        RegExp
      );
      expect(
        A11Y_ATTRIBUTES.connectionStatus.ariaLabelPattern.test(
          'Connection status: Connected'
        )
      ).toBe(true);
    });
  });

  describe('issueCard attributes', () => {
    it('has correct role', () => {
      expect(A11Y_ATTRIBUTES.issueCard.role).toBe('button');
    });

    it('has ariaLabelPattern as RegExp', () => {
      expect(A11Y_ATTRIBUTES.issueCard.ariaLabelPattern).toBeInstanceOf(RegExp);
      expect(
        A11Y_ATTRIBUTES.issueCard.ariaLabelPattern.test('Issue: Fix login bug')
      ).toBe(true);
    });
  });

  describe('statusColumn attributes', () => {
    it('has ariaLabelPattern as RegExp', () => {
      expect(A11Y_ATTRIBUTES.statusColumn.ariaLabelPattern).toBeInstanceOf(
        RegExp
      );
      expect(
        A11Y_ATTRIBUTES.statusColumn.ariaLabelPattern.test('Open issues')
      ).toBe(true);
      expect(
        A11Y_ATTRIBUTES.statusColumn.ariaLabelPattern.test('In Progress issues')
      ).toBe(true);
    });
  });

  describe('retryButton attributes', () => {
    it('has correct ariaLabel', () => {
      expect(A11Y_ATTRIBUTES.retryButton.ariaLabel).toBe('Retry connection now');
    });
  });
});

describe('Type exports', () => {
  describe('ConnectionStateKey type', () => {
    it('allows valid connection state keys', () => {
      const validKeys: ConnectionStateKey[] = [
        'connected',
        'connecting',
        'reconnecting',
        'disconnected',
      ];

      for (const key of validKeys) {
        expect(CONNECTION_STATES[key]).toBeDefined();
      }
    });

    it('can be used to index CONNECTION_STATES', () => {
      const key: ConnectionStateKey = 'connected';
      const state = CONNECTION_STATES[key];
      expect(state.dataState).toBe('connected');
    });
  });

  describe('IssueStatus type', () => {
    it('matches values in ISSUE_STATUSES array', () => {
      const statuses: IssueStatus[] = [
        'open',
        'in_progress',
        'review',
        'blocked',
        'closed',
      ];

      expect(statuses).toEqual([...ISSUE_STATUSES]);
    });

    it('can be used with ISSUE_STATUSES', () => {
      const status: IssueStatus = ISSUE_STATUSES[0];
      expect(status).toBe('open');
    });
  });

  describe('PriorityLevel type', () => {
    it('allows valid priority levels 0-4', () => {
      const validLevels: PriorityLevel[] = [0, 1, 2, 3, 4];

      for (const level of validLevels) {
        expect(PRIORITY_LEVELS[level]).toBeDefined();
      }
    });

    it('can be used to index PRIORITY_LEVELS', () => {
      const level: PriorityLevel = 0;
      const priority = PRIORITY_LEVELS[level];
      expect(priority.label).toBe('P0');
    });
  });
});
