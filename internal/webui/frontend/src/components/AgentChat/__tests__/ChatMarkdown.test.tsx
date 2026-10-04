/**
 * @vitest-environment jsdom
 */

import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const parsed = vi.hoisted(() => [] as string[]);
vi.mock("react-markdown", async (orig) => {
  const real = (await orig()) as { default: (p: object) => unknown };
  return {
    default: (props: { children: string }) => {
      parsed.push(props.children);
      return real.default(props);
    },
  };
});

import { ChatMarkdown, splitBlocks } from "../ChatMarkdown";

describe("splitBlocks", () => {
  it("splits at blank lines between top-level blocks", () => {
    expect(splitBlocks("# Title\n\nOne para.\n\nTwo para.")).toEqual([
      "# Title\n\n",
      "One para.\n\n",
      "Two para.",
    ]);
  });

  it("keeps a fenced code block with blank lines whole", () => {
    const md = "Intro\n\n```ts\nconst a = 1;\n\nconst b = 2;\n```\n\nAfter";
    expect(splitBlocks(md)).toEqual([
      "Intro\n\n",
      "```ts\nconst a = 1;\n\nconst b = 2;\n```\n\n",
      "After",
    ]);
  });

  it("keeps lists, quotes, indented continuations and tables whole", () => {
    const md = "- a\n\n- b\n\n  more b\n\n> q\n\n> r\n\n| x |\n|---|\n\n| y |";
    expect(splitBlocks(md)).toEqual([md]);
  });

  it("keeps an HTML block that spans a blank line whole", () => {
    const md = "<!--\n\nnote\n-->";
    expect(splitBlocks(md)).toEqual([md]);
  });

  it("does not split text that uses reference links", () => {
    const md = "See [x].\n\nMore.\n\n[x]: https://example.com";
    expect(splitBlocks(md)).toEqual([md]);
  });
});

describe("ChatMarkdown block memoisation", () => {
  it("re-parses only the last block as streamed text grows", () => {
    const head = "# Title\n\nFirst paragraph.\n\n";
    const { rerender, container } = render(
      <ChatMarkdown text={head + "Third"} streaming />,
    );
    parsed.length = 0;
    rerender(<ChatMarkdown text={head + "Third paragraph"} streaming />);
    expect(parsed).toEqual(["Third paragraph"]);
    expect(container.querySelector("h1")?.textContent).toBe("Title");
  });

  it("shows a caret at the end only while streaming", () => {
    const { container, rerender } = render(
      <ChatMarkdown text={"One.\n\n- a\n- b"} streaming />,
    );
    const caret = container.querySelector("[data-streaming-caret]");
    expect(caret?.parentElement?.textContent).toBe("b");
    rerender(<ChatMarkdown text={"One.\n\n- a\n- b"} />);
    expect(container.querySelector("[data-streaming-caret]")).toBeNull();
  });

  it("fades the freshly revealed run by opacity alone", () => {
    const { container } = render(
      <ChatMarkdown
        text={"Done.\n\nHello there"}
        streaming
        fresh={[{ from: 12, opacity: 0.25 }]}
      />,
    );
    const run = container.querySelector<HTMLElement>("[data-fresh]");
    expect(run?.textContent).toBe(" there");
    expect(run?.style.opacity).toBe("0.25");
    expect(run?.parentElement?.textContent).toBe("Hello there");
  });

  it("keeps each revealed run in the span it started in (no layout shift)", () => {
    const { container, rerender } = render(
      <ChatMarkdown
        text={"Hello there my"}
        streaming
        fresh={[{ from: 5, opacity: 0.5 }]}
      />,
    );
    const first = container.querySelector("[data-fresh]")!;
    expect(first.textContent).toBe(" there my");
    rerender(
      <ChatMarkdown
        text={"Hello there my friend"}
        streaming
        fresh={[
          { from: 5, opacity: 1 },
          { from: 14, opacity: 0.2 },
        ]}
      />,
    );
    expect(first.isConnected).toBe(true);
    expect(first.textContent).toBe(" there my");
    const next = container.querySelector("[data-fresh]")!;
    expect(next).not.toBe(first);
    expect(next.textContent).toBe(" friend");
  });
});
