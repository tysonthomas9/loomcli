import type { KeyboardEvent } from "react";

export const ROOT_KEY_ATTR = "data-root-key";
export const TREE_SCROLL_ATTR = "data-tree-scroll";

type QueryScope = Document | Element;

/**
 * Root toggles in DOM order, minus the ones that cannot take focus. An
 * unavailable checkout renders its row `disabled`, and stepping onto a
 * disabled button is a silent no-op — focus would stay put and the roots past
 * it would be unreachable by keyboard.
 */
function rootToggles(scope: QueryScope): HTMLElement[] {
  return Array.from(
    scope.querySelectorAll<HTMLElement>(`[${ROOT_KEY_ATTR}]`),
  ).filter((toggle) => !(toggle as HTMLButtonElement).disabled);
}

function queryScope(element: Element): QueryScope {
  return element.closest(`[${TREE_SCROLL_ATTR}]`) ?? document;
}

/** Focus the root toggle carrying this key. */
export function focusRootToggle(rootKey: string): void {
  const activeElement = document.activeElement;
  const scope = activeElement ? queryScope(activeElement) : document;
  rootToggles(scope)
    .find((toggle) => toggle.getAttribute(ROOT_KEY_ATTR) === rootKey)
    ?.focus();
}

export function handleRootToggleKeyDown(
  event: KeyboardEvent<HTMLElement>,
  opts: { expanded: boolean; onToggle: () => void },
): void {
  const current = event.currentTarget;
  const scope = queryScope(current);
  const toggles = rootToggles(scope);
  const currentIndex = toggles.indexOf(current);

  switch (event.key) {
    case "ArrowDown":
      event.preventDefault();
      toggles[Math.min(toggles.length - 1, currentIndex + 1)]?.focus();
      break;
    case "ArrowUp":
      event.preventDefault();
      toggles[Math.max(0, currentIndex - 1)]?.focus();
      break;
    case "Home":
      event.preventDefault();
      toggles[0]?.focus();
      break;
    case "End":
      event.preventDefault();
      toggles[toggles.length - 1]?.focus();
      break;
    case "ArrowRight":
      event.preventDefault();
      if (!opts.expanded) {
        opts.onToggle();
        break;
      }
      for (const candidate of scope.querySelectorAll<HTMLElement>(
        `[${ROOT_KEY_ATTR}],[role="tree"]`,
      )) {
        if (candidate === current) continue;
        if (
          current.compareDocumentPosition(candidate) &
          Node.DOCUMENT_POSITION_FOLLOWING
        ) {
          if (candidate.hasAttribute(ROOT_KEY_ATTR)) break;
          candidate.focus();
          break;
        }
      }
      break;
    case "ArrowLeft":
      event.preventDefault();
      if (opts.expanded) opts.onToggle();
      break;
  }
}
