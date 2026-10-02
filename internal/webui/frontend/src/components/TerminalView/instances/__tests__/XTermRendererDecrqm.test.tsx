/**
 * @vitest-environment jsdom
 */

import { render, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { XTermRenderer, type XTermRendererHandle } from "../XTermRenderer";

// Real xterm, no mock: OpenCode 2's TUI starts with DECRQM queries, which the
// minified xterm 6.0.0 handler answers by throwing and freezing the pane.
it("answers DECRQM itself and keeps rendering later output", async () => {
  // jsdom lacks matchMedia, which xterm's open() reads for the pixel ratio.
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addListener: vi.fn(),
    removeListener: vi.fn(),
  }));
  const onReady = vi.fn();
  const onData = vi.fn();
  render(
    <XTermRenderer
      onReady={onReady}
      onDispose={vi.fn()}
      onData={onData}
      onBinary={vi.fn()}
      onResize={vi.fn()}
    />,
  );
  await waitFor(() => expect(onReady).toHaveBeenCalledOnce());
  const handle = onReady.mock.calls[0]?.[0] as XTermRendererHandle;

  handle.write("\x1b[?1016$p\x1b[?2026$p\x1b[4$p");
  handle.write("after\x1b[?2004$p");

  await waitFor(() =>
    expect(onData.mock.calls.map((c) => c[0])).toEqual([
      "\x1b[?1016;0$y",
      "\x1b[?2026;0$y",
      "\x1b[4;0$y",
      "\x1b[?2004;0$y",
    ]),
  );
});
