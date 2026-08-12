/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup } from "@testing-library/react";

// --- Track mock instances via module-level container ---
// vi.mock factories are hoisted, so we use vi.hoisted to share state
const mocks = vi.hoisted(() => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const instances: { lastView: any } = { lastView: null };
  const openSearchPanel = vi.fn();
  const closeSearchPanel = vi.fn();
  const resizeObserver = {
    observe: vi.fn(),
    disconnect: vi.fn(),
  };
  return { instances, openSearchPanel, closeSearchPanel, resizeObserver };
});

vi.mock("@codemirror/state", () => ({
  EditorState: {
    create: vi.fn(() => ({
      doc: { toString: () => "", length: 0 },
      selection: { main: { head: 0 } },
    })),
    readOnly: { of: vi.fn(() => []) },
  },
  Compartment: class {
    of = vi.fn(() => []);
    reconfigure = vi.fn(() => ({ type: "reconfigure" }));
  },
  RangeSetBuilder: class {
    add = vi.fn();
    finish = vi.fn(() => []);
  },
}));

vi.mock("@codemirror/view", () => ({
  EditorView: class {
    state = {
      doc: {
        toString: () => "",
        length: 0,
        lineAt: () => ({ number: 1, from: 0, to: 0 }),
      },
      selection: { main: { head: 0 } },
    };
    dispatch = vi.fn();
    destroy = vi.fn();
    requestMeasure = vi.fn();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    constructor(config: any) {
      if (config.parent) {
        const editorDiv = document.createElement("div");
        editorDiv.className = "cm-editor";
        config.parent.appendChild(editorDiv);
      }
      mocks.instances.lastView = this;
    }
    static theme = vi.fn(() => []);
    static updateListener = { of: vi.fn(() => []) };
    static editable = { of: vi.fn(() => []) };
    static baseTheme = vi.fn(() => []);
  },
  keymap: { of: vi.fn(() => []) },
  placeholder: vi.fn(() => []),
  lineNumbers: vi.fn(() => []),
  gutter: vi.fn(() => []),
  GutterMarker: class {
    elementClass = "";
    eq = vi.fn(() => false);
    toDOM = vi.fn(() => document.createElement("span"));
  },
  Decoration: {
    none: [],
    widget: vi.fn(() => ({})),
  },
  ViewPlugin: {
    fromClass: vi.fn(() => []),
  },
  WidgetType: class {
    eq = vi.fn(() => false);
    toDOM = vi.fn(() => document.createElement("span"));
  },
}));

vi.mock("@codemirror/commands", () => ({
  defaultKeymap: [],
  history: vi.fn(() => []),
  historyKeymap: [],
}));

vi.mock("@codemirror/search", () => ({
  search: vi.fn(() => []),
  searchKeymap: [],
  gotoLine: vi.fn(() => true),
  openSearchPanel: mocks.openSearchPanel,
  closeSearchPanel: mocks.closeSearchPanel,
}));

vi.mock("@codemirror/lang-go", () => ({ go: vi.fn(() => []) }));
vi.mock("@codemirror/lang-json", () => ({ json: vi.fn(() => []) }));
vi.mock("@codemirror/lang-yaml", () => ({ yaml: vi.fn(() => []) }));
vi.mock("@codemirror/lang-markdown", () => ({ markdown: vi.fn(() => []) }));

// Mock ResizeObserver (not available in jsdom)
vi.stubGlobal(
  "ResizeObserver",
  class {
    observe = mocks.resizeObserver.observe;
    disconnect = mocks.resizeObserver.disconnect;
    unobserve = vi.fn();
  },
);

// Import after mocks
const { EditorState } = await import("@codemirror/state");
const { CodeMirrorEditor } = await import("../CodeMirrorEditor");

describe("CodeMirrorEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    cleanup();
  });

  it("renders container div with data-testid", () => {
    const { getByTestId } = render(<CodeMirrorEditor value="hello" />);
    expect(getByTestId("codemirror-editor")).toBeDefined();
  });

  it("creates EditorView on mount", () => {
    render(<CodeMirrorEditor value="hello world" />);
    expect(EditorState.create).toHaveBeenCalledWith(
      expect.objectContaining({ doc: "hello world" }),
    );
    expect(mocks.instances.lastView).toBeDefined();
  });

  it("destroys EditorView on unmount", () => {
    const { unmount } = render(<CodeMirrorEditor value="" />);
    const view = mocks.instances.lastView;
    unmount();
    expect(view.destroy).toHaveBeenCalled();
  });

  it("creates EditorState with readOnly extension when readOnly=true", () => {
    render(<CodeMirrorEditor value="" readOnly={true} />);
    expect(EditorState.readOnly.of).toHaveBeenCalledWith(true);
  });

  it("dispatches language reconfiguration on language prop change", () => {
    const { rerender } = render(<CodeMirrorEditor value="" language="go" />);
    const view = mocks.instances.lastView;
    vi.clearAllMocks();

    rerender(<CodeMirrorEditor value="" language="json" />);
    expect(view.dispatch).toHaveBeenCalledWith(
      expect.objectContaining({ effects: expect.anything() }),
    );
  });

  it("dispatches readOnly reconfiguration on prop change", () => {
    const { rerender } = render(<CodeMirrorEditor value="" readOnly={false} />);
    const view = mocks.instances.lastView;
    vi.clearAllMocks();

    rerender(<CodeMirrorEditor value="" readOnly={true} />);
    expect(view.dispatch).toHaveBeenCalled();
    expect(EditorState.readOnly.of).toHaveBeenCalledWith(true);
  });

  it("syncs external value when it differs from current doc", () => {
    const { rerender } = render(<CodeMirrorEditor value="old" />);
    const view = mocks.instances.lastView;
    view.state = { doc: { toString: () => "old", length: 3 } };
    vi.clearAllMocks();

    rerender(<CodeMirrorEditor value="new content" />);
    expect(view.dispatch).toHaveBeenCalledWith(
      expect.objectContaining({
        changes: { from: 0, to: 3, insert: "new content" },
      }),
    );
  });

  it("skips value sync when value matches current doc", () => {
    const { rerender } = render(<CodeMirrorEditor value="same" />);
    const view = mocks.instances.lastView;
    view.state = { doc: { toString: () => "same", length: 4 } };
    vi.clearAllMocks();

    rerender(<CodeMirrorEditor value="same" />);
    const valueSyncCall = view.dispatch.mock.calls.find(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      (call: any[]) => call[0]?.changes !== undefined,
    );
    expect(valueSyncCall).toBeUndefined();
  });

  it("calls openSearchPanel when searchOpen becomes true", () => {
    const { rerender } = render(
      <CodeMirrorEditor value="" searchOpen={false} />,
    );
    vi.clearAllMocks();

    rerender(<CodeMirrorEditor value="" searchOpen={true} />);
    expect(mocks.openSearchPanel).toHaveBeenCalledWith(
      mocks.instances.lastView,
    );
  });

  it("calls closeSearchPanel when searchOpen becomes false", () => {
    const { rerender } = render(
      <CodeMirrorEditor value="" searchOpen={true} />,
    );
    vi.clearAllMocks();

    rerender(<CodeMirrorEditor value="" searchOpen={false} />);
    expect(mocks.closeSearchPanel).toHaveBeenCalledWith(
      mocks.instances.lastView,
    );
  });

  it("handles unknown language gracefully", () => {
    expect(() => {
      render(<CodeMirrorEditor value="" language="brainfuck" />);
    }).not.toThrow();
  });

  it("applies className to container", () => {
    const { getByTestId } = render(
      <CodeMirrorEditor value="" className="custom-class" />,
    );
    const container = getByTestId("codemirror-editor");
    expect(container.className).toContain("custom-class");
  });

  it("sets data-readonly attribute when readOnly is true", () => {
    const { getByTestId } = render(
      <CodeMirrorEditor value="" readOnly={true} />,
    );
    const container = getByTestId("codemirror-editor");
    expect(container.getAttribute("data-readonly")).toBeDefined();
  });

  it("does not set data-readonly when readOnly is false", () => {
    const { getByTestId } = render(
      <CodeMirrorEditor value="" readOnly={false} />,
    );
    const container = getByTestId("codemirror-editor");
    expect(container.getAttribute("data-readonly")).toBeNull();
  });

  it("sets up ResizeObserver on mount and disconnects on unmount", () => {
    const { unmount } = render(<CodeMirrorEditor value="" />);
    expect(mocks.resizeObserver.observe).toHaveBeenCalled();
    unmount();
    expect(mocks.resizeObserver.disconnect).toHaveBeenCalled();
  });
});
