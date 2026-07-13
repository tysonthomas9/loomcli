import { vi } from "vitest";

if (typeof window !== "undefined" && !window.matchMedia) {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches:
        query.includes("prefers-color-scheme: dark") ||
        query.includes("min-width"),
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class ResizeObserver {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  };
}

if (
  typeof Element !== "undefined" &&
  typeof Element.prototype.scrollIntoView !== "function"
) {
  Element.prototype.scrollIntoView = vi.fn();
}

if (typeof HTMLCanvasElement !== "undefined") {
  HTMLCanvasElement.prototype.getContext = vi.fn().mockImplementation(() => ({
    font: "",
    measureText: (text: string) => ({ width: text.length * 8 }),
  }));
}
