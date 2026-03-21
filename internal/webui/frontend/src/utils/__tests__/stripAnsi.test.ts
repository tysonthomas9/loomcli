/**
 * Unit tests for stripAnsi utility.
 */

import { describe, it, expect } from "vitest";

import { stripAnsi } from "../stripAnsi";

describe("stripAnsi", () => {
  it("returns plain text unchanged", () => {
    expect(stripAnsi("hello world")).toBe("hello world");
  });

  it("returns empty string for empty input", () => {
    expect(stripAnsi("")).toBe("");
  });

  it("strips SGR color codes (e.g. bold, red, reset)", () => {
    // Bold
    expect(stripAnsi("\x1b[1mBold\x1b[0m")).toBe("Bold");
    // Red foreground
    expect(stripAnsi("\x1b[31mRed text\x1b[0m")).toBe("Red text");
    // 256-color
    expect(stripAnsi("\x1b[38;5;196mColored\x1b[0m")).toBe("Colored");
    // True-color (24-bit)
    expect(stripAnsi("\x1b[38;2;255;0;0mTrueColor\x1b[0m")).toBe("TrueColor");
  });

  it("strips cursor movement codes", () => {
    // Cursor up
    expect(stripAnsi("\x1b[2Ahello")).toBe("hello");
    // Cursor down
    expect(stripAnsi("\x1b[3Bworld")).toBe("world");
    // Cursor forward
    expect(stripAnsi("\x1b[5Ctext")).toBe("text");
    // Cursor back
    expect(stripAnsi("\x1b[1Dback")).toBe("back");
    // Cursor position
    expect(stripAnsi("\x1b[10;20Hhere")).toBe("here");
  });

  it("strips erase sequences", () => {
    // Erase in display
    expect(stripAnsi("\x1b[2Jcleared")).toBe("cleared");
    // Erase in line
    expect(stripAnsi("\x1b[Ktrailing")).toBe("trailing");
  });

  it("strips OSC sequences (BEL-terminated)", () => {
    // Set terminal title
    expect(stripAnsi("\x1b]0;My Title\x07visible")).toBe("visible");
  });

  it("strips OSC sequences (ST-terminated)", () => {
    // Set terminal title with ST terminator
    expect(stripAnsi("\x1b]0;My Title\x1b\\visible")).toBe("visible");
  });

  it("strips OSC hyperlink sequences", () => {
    expect(
      stripAnsi("\x1b]8;;https://example.com\x1b\\Link Text\x1b]8;;\x1b\\"),
    ).toBe("Link Text");
  });

  it("strips character set designation sequences", () => {
    // G0 charset
    expect(stripAnsi("\x1b(Btext")).toBe("text");
    // G1 charset
    expect(stripAnsi("\x1b)0text")).toBe("text");
  });

  it("strips mixed ANSI codes from realistic terminal output", () => {
    const input =
      "\x1b[1m\x1b[32m$\x1b[0m \x1b[34mls\x1b[0m -la\x1b[K\n" +
      "\x1b[36mfile.txt\x1b[0m  \x1b[32mdir/\x1b[0m";
    expect(stripAnsi(input)).toBe("$ ls -la\nfile.txt  dir/");
  });

  it("returns empty string when input contains only ANSI codes", () => {
    expect(stripAnsi("\x1b[31m\x1b[1m\x1b[0m")).toBe("");
    expect(stripAnsi("\x1b[2J\x1b[H")).toBe("");
  });

  it("handles multiple consecutive ANSI codes", () => {
    expect(stripAnsi("\x1b[1m\x1b[31m\x1b[4mStyled\x1b[0m")).toBe("Styled");
  });

  it("preserves newlines and whitespace between ANSI codes", () => {
    expect(stripAnsi("\x1b[31mline1\x1b[0m\n\x1b[32mline2\x1b[0m")).toBe(
      "line1\nline2",
    );
    expect(stripAnsi("  \x1b[1mspaced\x1b[0m  ")).toBe("  spaced  ");
  });

  it("handles text with brackets that are not ANSI sequences", () => {
    expect(stripAnsi("[not an escape]")).toBe("[not an escape]");
    expect(stripAnsi("array[0]")).toBe("array[0]");
  });
});
