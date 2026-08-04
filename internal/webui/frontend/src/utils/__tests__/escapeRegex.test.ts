/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for escapeRegex utility.
 */

import { describe, it, expect } from "vitest";

import { escapeRegex } from "../escapeRegex";

describe("escapeRegex", () => {
  it("returns empty string for empty input", () => {
    expect(escapeRegex("")).toBe("");
  });

  it("passes through non-special characters unchanged", () => {
    expect(escapeRegex("hello world")).toBe("hello world");
  });

  it("passes through letters and digits unchanged", () => {
    expect(escapeRegex("abc123XYZ")).toBe("abc123XYZ");
  });

  describe("escapes each special regex character", () => {
    it("escapes dot (.)", () => {
      expect(escapeRegex(".")).toBe("\\.");
    });

    it("escapes asterisk (*)", () => {
      expect(escapeRegex("*")).toBe("\\*");
    });

    it("escapes plus (+)", () => {
      expect(escapeRegex("+")).toBe("\\+");
    });

    it("escapes question mark (?)", () => {
      expect(escapeRegex("?")).toBe("\\?");
    });

    it("escapes caret (^)", () => {
      expect(escapeRegex("^")).toBe("\\^");
    });

    it("escapes dollar sign ($)", () => {
      expect(escapeRegex("$")).toBe("\\$");
    });

    it("escapes opening brace ({)", () => {
      expect(escapeRegex("{")).toBe("\\{");
    });

    it("escapes closing brace (})", () => {
      expect(escapeRegex("}")).toBe("\\}");
    });

    it("escapes opening parenthesis (()", () => {
      expect(escapeRegex("(")).toBe("\\(");
    });

    it("escapes closing parenthesis ())", () => {
      expect(escapeRegex(")")).toBe("\\)");
    });

    it("escapes pipe (|)", () => {
      expect(escapeRegex("|")).toBe("\\|");
    });

    it("escapes opening bracket ([)", () => {
      expect(escapeRegex("[")).toBe("\\[");
    });

    it("escapes closing bracket (])", () => {
      expect(escapeRegex("]")).toBe("\\]");
    });

    it("escapes backslash (\\)", () => {
      expect(escapeRegex("\\")).toBe("\\\\");
    });
  });

  it("escapes multiple special characters in a string", () => {
    expect(escapeRegex("file.ts (v2.0)")).toBe("file\\.ts \\(v2\\.0\\)");
  });

  it("escapes all special characters together", () => {
    const input = ".*+?^${}()|[]\\";
    const expected = "\\.\\*\\+\\?\\^\\$\\{\\}\\(\\)\\|\\[\\]\\\\";
    expect(escapeRegex(input)).toBe(expected);
  });

  it("produces a string safe for RegExp constructor", () => {
    const dangerous = "price is $9.99 (USD)";
    const escaped = escapeRegex(dangerous);
    const regex = new RegExp(escaped);

    expect(regex.test("price is $9.99 (USD)")).toBe(true);
    expect(regex.test("price is X9X99 XUSDX")).toBe(false);
  });

  it("handles string with mixed special and non-special characters", () => {
    expect(escapeRegex("a.b*c+d")).toBe("a\\.b\\*c\\+d");
  });
});
