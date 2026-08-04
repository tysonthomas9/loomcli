/**
 * Accessibility test helpers.
 * Shared utilities for automated a11y testing with jest-axe.
 */

// @vitest-environment jsdom

import type { RenderResult } from "@testing-library/react";
import { axe, toHaveNoViolations } from "jest-axe";
import { expect } from "vitest";

// Extend vitest matchers with jest-axe
expect.extend(toHaveNoViolations);

/**
 * Assert that a rendered component has no axe accessibility violations.
 * Disables color-contrast rule since jsdom doesn't render pixels.
 */
export async function expectNoA11yViolations(
  container: HTMLElement | RenderResult,
): Promise<void> {
  const el = "container" in container ? container.container : container;
  const results = await axe(el, {
    rules: {
      // jsdom can't compute actual pixel colors
      "color-contrast": { enabled: false },
    },
  });
  expect(results).toHaveNoViolations();
}

/**
 * Assert that an element has a non-empty aria-label.
 */
export function expectAriaLabel(element: HTMLElement): void {
  const label = element.getAttribute("aria-label");
  expect(label).toBeTruthy();
  expect(label!.trim().length).toBeGreaterThan(0);
}

/**
 * Assert that a specific text is present in the DOM for screen readers,
 * regardless of visual visibility.
 */
export function expectScreenReaderText(
  container: HTMLElement,
  text: string,
): void {
  expect(container.textContent).toContain(text);
}

/**
 * Get all elements with role="button" that lack an accessible name.
 */
export function findButtonsWithoutLabels(
  container: HTMLElement,
): HTMLElement[] {
  const buttons = container.querySelectorAll(
    'button, [role="button"]',
  ) as NodeListOf<HTMLElement>;
  return Array.from(buttons).filter((btn) => {
    const ariaLabel = btn.getAttribute("aria-label");
    const ariaLabelledBy = btn.getAttribute("aria-labelledby");
    const textContent = btn.textContent?.trim();
    return !ariaLabel && !ariaLabelledBy && !textContent;
  });
}
