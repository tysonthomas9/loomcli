import { describe, it, expect, vi, afterEach } from "vitest";
import { parseRetryAfter } from "../retryAfter";

describe("parseRetryAfter", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("parses delta-seconds into milliseconds", () => {
    expect(parseRetryAfter("12")).toBe(12000);
    expect(parseRetryAfter("0")).toBe(0);
    expect(parseRetryAfter("  7  ")).toBe(7000);
  });

  it("returns undefined for absent or unparseable values", () => {
    expect(parseRetryAfter(null)).toBeUndefined();
    expect(parseRetryAfter("")).toBeUndefined();
    expect(parseRetryAfter("soon")).toBeUndefined();
  });

  it("returns undefined for a negative delta", () => {
    expect(parseRetryAfter("-5")).toBeUndefined();
  });

  it("clamps an absurd delta to five minutes", () => {
    expect(parseRetryAfter("99999")).toBe(300000);
  });

  it("parses an HTTP-date in the future as a delta", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
    expect(parseRetryAfter("Thu, 01 Jan 2026 00:00:30 GMT")).toBe(30000);
  });

  it("clamps an HTTP-date in the past to zero", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
    expect(parseRetryAfter("Wed, 31 Dec 2025 23:59:00 GMT")).toBe(0);
  });
});
