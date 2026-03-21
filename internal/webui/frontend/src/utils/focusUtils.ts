/**
 * Shared focus management utilities.
 */

const FOCUSABLE_SELECTOR = [
  "a[href]",
  "button:not(:disabled)",
  "input:not(:disabled)",
  "select:not(:disabled)",
  "textarea:not(:disabled)",
  '[tabindex]:not([tabindex="-1"]):not(:disabled)',
  '[contenteditable="true"]',
].join(", ");

/**
 * Returns true if the element is visible in the DOM and not hidden
 * by aria-hidden on an ancestor.
 */
export function isFocusable(element: HTMLElement): boolean {
  // Check offsetParent for visibility (null means hidden via display:none,
  // except for fixed/sticky positioned elements)
  if (element.offsetParent === null) {
    const style = getComputedStyle(element);
    if (style.position !== "fixed" && style.position !== "sticky") {
      return false;
    }
  }

  // Check for aria-hidden on the element or any ancestor
  let current: HTMLElement | null = element;
  while (current) {
    if (current.getAttribute("aria-hidden") === "true") {
      return false;
    }
    current = current.parentElement;
  }

  return true;
}

/**
 * Returns all focusable elements within a container, in DOM order.
 * Excludes hidden and disabled elements.
 */
export function getFocusableElements(container: HTMLElement): HTMLElement[] {
  const candidates = Array.from(
    container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR),
  );
  return candidates.filter(isFocusable);
}
