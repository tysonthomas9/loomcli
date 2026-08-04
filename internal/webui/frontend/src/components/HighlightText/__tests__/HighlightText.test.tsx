/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for HighlightText component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { HighlightText } from "../HighlightText";

describe("HighlightText", () => {
  describe("plain text rendering (no highlighting)", () => {
    it("renders plain text when searchTerm is empty", () => {
      render(<HighlightText text="Hello World" searchTerm="" />);

      expect(screen.getByText("Hello World")).toBeInTheDocument();
      expect(document.querySelectorAll("mark")).toHaveLength(0);
    });

    it("renders plain text when there is no match", () => {
      render(<HighlightText text="Hello World" searchTerm="xyz" />);

      expect(screen.getByText("Hello World")).toBeInTheDocument();
      expect(document.querySelectorAll("mark")).toHaveLength(0);
    });

    it("renders nothing visible when text is empty", () => {
      const { container } = render(
        <HighlightText text="" searchTerm="search" />,
      );

      expect(container.textContent).toBe("");
      expect(document.querySelectorAll("mark")).toHaveLength(0);
    });

    it("renders nothing visible when both text and searchTerm are empty", () => {
      const { container } = render(<HighlightText text="" searchTerm="" />);

      expect(container.textContent).toBe("");
    });
  });

  describe("single match highlighting", () => {
    it("wraps a single match in a <mark> tag", () => {
      render(<HighlightText text="Hello World" searchTerm="World" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("World");
    });

    it("renders surrounding text outside <mark>", () => {
      const { container } = render(
        <HighlightText text="Hello World" searchTerm="World" />,
      );

      expect(container.textContent).toBe("Hello World");
      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("World");
    });

    it("highlights match at the beginning of text", () => {
      render(<HighlightText text="Hello World" searchTerm="Hello" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("Hello");
    });

    it("highlights match at the end of text", () => {
      render(<HighlightText text="Hello World" searchTerm="World" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("World");
    });

    it("highlights when searchTerm matches entire text", () => {
      render(<HighlightText text="Hello" searchTerm="Hello" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("Hello");
    });
  });

  describe("multiple match highlighting", () => {
    it("wraps multiple occurrences in separate <mark> tags", () => {
      render(<HighlightText text="foo bar foo baz foo" searchTerm="foo" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(3);
      marks.forEach((mark) => {
        expect(mark).toHaveTextContent("foo");
      });
    });

    it("preserves text between matches", () => {
      const { container } = render(
        <HighlightText text="foo bar foo" searchTerm="foo" />,
      );

      expect(container.textContent).toBe("foo bar foo");
    });
  });

  describe("case-insensitive matching", () => {
    it("matches regardless of case", () => {
      render(<HighlightText text="Hello HELLO hello" searchTerm="hello" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(3);
    });

    it("preserves original case in highlighted text", () => {
      render(<HighlightText text="Hello HELLO hello" searchTerm="hello" />);

      const marks = document.querySelectorAll("mark");
      expect(marks[0]).toHaveTextContent("Hello");
      expect(marks[1]).toHaveTextContent("HELLO");
      expect(marks[2]).toHaveTextContent("hello");
    });

    it("matches with mixed case searchTerm", () => {
      render(<HighlightText text="hello world" searchTerm="HeLLo" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("hello");
    });
  });

  describe("regex special characters in searchTerm", () => {
    it("handles dot in searchTerm without treating it as regex", () => {
      render(<HighlightText text="file.ts and filets" searchTerm="file.ts" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("file.ts");
    });

    it("handles parentheses in searchTerm", () => {
      render(<HighlightText text="call foo(bar) now" searchTerm="foo(bar)" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("foo(bar)");
    });

    it("handles brackets in searchTerm", () => {
      render(<HighlightText text="arr[0] is first" searchTerm="arr[0]" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("arr[0]");
    });

    it("handles asterisk in searchTerm", () => {
      render(<HighlightText text="use *.ts glob" searchTerm="*.ts" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("*.ts");
    });

    it("handles dollar sign in searchTerm", () => {
      render(<HighlightText text="price is $9.99" searchTerm="$9.99" />);

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("$9.99");
    });

    it("does not throw on complex regex patterns as searchTerm", () => {
      expect(() => {
        render(
          <HighlightText
            text="some (test) [value]"
            searchTerm="(test) [value]"
          />,
        );
      }).not.toThrow();

      const marks = document.querySelectorAll("mark");
      expect(marks).toHaveLength(1);
      expect(marks[0]).toHaveTextContent("(test) [value]");
    });
  });

  describe("CSS module styling", () => {
    it("applies highlight class to <mark> elements", () => {
      render(<HighlightText text="Hello World" searchTerm="World" />);

      const mark = document.querySelector("mark");
      expect(mark).toBeInTheDocument();
      expect(mark?.className).toMatch(/highlight/);
    });
  });
});
