/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for focusUtils utility functions.
 */

import { describe, it, expect, beforeEach, afterEach } from "vitest";

import { getFocusableElements, isFocusable } from "../focusUtils";

/**
 * In jsdom, offsetParent is always null which makes isFocusable treat every
 * element as hidden (display:none). This helper patches offsetParent on all
 * child elements inside a container so they appear visible to isFocusable.
 */
function markChildrenVisible(parent: HTMLElement): void {
  parent.querySelectorAll("*").forEach((el) => {
    Object.defineProperty(el, "offsetParent", {
      get: () => parent,
      configurable: true,
    });
  });
}

describe("focusUtils", () => {
  let container: HTMLDivElement;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
  });

  afterEach(() => {
    document.body.removeChild(container);
  });

  describe("getFocusableElements", () => {
    it("returns buttons", () => {
      container.innerHTML = "<button>Click me</button>";
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.tagName).toBe("BUTTON");
    });

    it("returns links with href", () => {
      container.innerHTML = '<a href="https://example.com">Link</a>';
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.tagName).toBe("A");
    });

    it("does not return links without href", () => {
      container.innerHTML = "<a>No href</a>";
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(0);
    });

    it("returns input elements", () => {
      container.innerHTML = '<input type="text" />';
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.tagName).toBe("INPUT");
    });

    it("returns select elements", () => {
      container.innerHTML = "<select><option>A</option></select>";
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.tagName).toBe("SELECT");
    });

    it("returns textarea elements", () => {
      container.innerHTML = "<textarea></textarea>";
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.tagName).toBe("TEXTAREA");
    });

    it("returns contenteditable elements", () => {
      container.innerHTML = '<div contenteditable="true">Editable</div>';
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
    });

    it("returns all focusable element types from a mixed container", () => {
      container.innerHTML = `
        <button>First</button>
        <input type="text" />
        <a href="#">Link</a>
        <textarea></textarea>
        <select><option>Opt</option></select>
      `;
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(5);
      const tagNames = elements.map((el) => el.tagName).sort();
      expect(tagNames).toEqual(["A", "BUTTON", "INPUT", "SELECT", "TEXTAREA"]);
    });

    it("excludes disabled buttons", () => {
      container.innerHTML = `
        <button>Enabled</button>
        <button disabled>Disabled</button>
      `;
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.textContent).toBe("Enabled");
    });

    it("excludes disabled inputs", () => {
      container.innerHTML = `
        <input type="text" />
        <input type="text" disabled />
      `;
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
    });

    it("excludes disabled selects", () => {
      container.innerHTML = `
        <select><option>A</option></select>
        <select disabled><option>B</option></select>
      `;
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
    });

    it("excludes disabled textareas", () => {
      container.innerHTML = `
        <textarea></textarea>
        <textarea disabled></textarea>
      `;
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
    });

    it("excludes elements with tabindex=-1", () => {
      container.innerHTML = `
        <button>Focusable</button>
        <div tabindex="-1">Not focusable via Tab</div>
      `;
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.tagName).toBe("BUTTON");
    });

    it("includes elements with tabindex=0", () => {
      container.innerHTML = '<div tabindex="0">Focusable div</div>';
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.textContent).toBe("Focusable div");
    });

    it("includes elements with positive tabindex", () => {
      container.innerHTML = '<div tabindex="1">Custom tab order</div>';
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
    });

    it("excludes elements inside aria-hidden ancestor", () => {
      container.innerHTML = `
        <button>Visible</button>
        <div aria-hidden="true">
          <button>Hidden</button>
        </div>
      `;
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.textContent).toBe("Visible");
    });

    it("excludes elements with aria-hidden on themselves", () => {
      container.innerHTML = `
        <button>Visible</button>
        <button aria-hidden="true">Self hidden</button>
      `;
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(1);
      expect(elements[0]!.textContent).toBe("Visible");
    });

    it("returns empty array for container with no focusable elements", () => {
      container.innerHTML = "<div><span>Just text</span></div>";
      markChildrenVisible(container);
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(0);
    });

    it("returns empty array for empty container", () => {
      const elements = getFocusableElements(container);
      expect(elements).toHaveLength(0);
    });
  });

  describe("isFocusable", () => {
    it("returns true for a visible element (non-null offsetParent)", () => {
      const button = document.createElement("button");
      button.textContent = "Click";
      container.appendChild(button);
      Object.defineProperty(button, "offsetParent", {
        get: () => container,
        configurable: true,
      });
      expect(isFocusable(button)).toBe(true);
    });

    it("returns false for element with aria-hidden=true", () => {
      const button = document.createElement("button");
      button.setAttribute("aria-hidden", "true");
      container.appendChild(button);
      Object.defineProperty(button, "offsetParent", {
        get: () => container,
        configurable: true,
      });
      expect(isFocusable(button)).toBe(false);
    });

    it("returns false for element inside aria-hidden ancestor", () => {
      const wrapper = document.createElement("div");
      wrapper.setAttribute("aria-hidden", "true");
      const button = document.createElement("button");
      wrapper.appendChild(button);
      container.appendChild(wrapper);
      Object.defineProperty(button, "offsetParent", {
        get: () => container,
        configurable: true,
      });
      expect(isFocusable(button)).toBe(false);
    });

    it("returns true when aria-hidden=false", () => {
      const button = document.createElement("button");
      button.setAttribute("aria-hidden", "false");
      container.appendChild(button);
      Object.defineProperty(button, "offsetParent", {
        get: () => container,
        configurable: true,
      });
      expect(isFocusable(button)).toBe(true);
    });

    it("returns false for element with display:none (null offsetParent, static position)", () => {
      const button = document.createElement("button");
      container.appendChild(button);
      Object.defineProperty(button, "offsetParent", {
        get: () => null,
        configurable: true,
      });
      const originalGetComputedStyle = window.getComputedStyle;
      window.getComputedStyle = (el: Element) => {
        if (el === button) {
          return { position: "static" } as CSSStyleDeclaration;
        }
        return originalGetComputedStyle(el);
      };

      expect(isFocusable(button)).toBe(false);

      window.getComputedStyle = originalGetComputedStyle;
    });

    it("returns true for fixed-position element with null offsetParent", () => {
      const button = document.createElement("button");
      container.appendChild(button);
      Object.defineProperty(button, "offsetParent", {
        get: () => null,
        configurable: true,
      });
      const originalGetComputedStyle = window.getComputedStyle;
      window.getComputedStyle = (el: Element) => {
        if (el === button) {
          return { position: "fixed" } as CSSStyleDeclaration;
        }
        return originalGetComputedStyle(el);
      };

      expect(isFocusable(button)).toBe(true);

      window.getComputedStyle = originalGetComputedStyle;
    });

    it("returns true for sticky-position element with null offsetParent", () => {
      const button = document.createElement("button");
      container.appendChild(button);
      Object.defineProperty(button, "offsetParent", {
        get: () => null,
        configurable: true,
      });
      const originalGetComputedStyle = window.getComputedStyle;
      window.getComputedStyle = (el: Element) => {
        if (el === button) {
          return { position: "sticky" } as CSSStyleDeclaration;
        }
        return originalGetComputedStyle(el);
      };

      expect(isFocusable(button)).toBe(true);

      window.getComputedStyle = originalGetComputedStyle;
    });

    it("returns true for element with a non-null offsetParent", () => {
      const button = document.createElement("button");
      container.appendChild(button);
      Object.defineProperty(button, "offsetParent", {
        get: () => container,
        configurable: true,
      });

      expect(isFocusable(button)).toBe(true);
    });

    it("returns false for deeply nested element under aria-hidden", () => {
      const level1 = document.createElement("div");
      const level2 = document.createElement("div");
      level2.setAttribute("aria-hidden", "true");
      const level3 = document.createElement("div");
      const button = document.createElement("button");
      level3.appendChild(button);
      level2.appendChild(level3);
      level1.appendChild(level2);
      container.appendChild(level1);
      Object.defineProperty(button, "offsetParent", {
        get: () => container,
        configurable: true,
      });

      expect(isFocusable(button)).toBe(false);
    });
  });
});
