import { describe, expect, it } from "vitest";

import { parseTerminalControlFrame } from "../terminalControlFrame";

describe("parseTerminalControlFrame", () => {
  it("parses the attach envelope the server sends after a replacement", () => {
    const raw = JSON.stringify({
      type: "attach",
      reattached: false,
      replaced: true,
      replaced_at: "2026-08-14T16:52:03Z",
      replaced_reason: "server_restart",
    });

    expect(parseTerminalControlFrame(raw)).toEqual({
      type: "attach",
      reattached: false,
      replaced: true,
      replaced_at: "2026-08-14T16:52:03Z",
      replaced_reason: "server_restart",
    });
  });

  it("parses a plain attach with no replacement fields", () => {
    const raw = JSON.stringify({
      type: "attach",
      reattached: true,
      replaced: false,
    });

    expect(parseTerminalControlFrame(raw)).toEqual({
      type: "attach",
      reattached: true,
      replaced: false,
    });
  });

  it("defaults missing booleans to false rather than undefined", () => {
    expect(parseTerminalControlFrame('{"type":"attach"}')).toEqual({
      type: "attach",
      reattached: false,
      replaced: false,
    });
  });

  it("returns null for valid JSON carrying an unknown type", () => {
    expect(
      parseTerminalControlFrame('{"type":"resize","cols":80,"rows":24}'),
    ).toBeNull();
  });

  it("returns null for a JSON object with no type at all", () => {
    expect(parseTerminalControlFrame('{"replaced":true}')).toBeNull();
  });

  it("returns null for non-JSON terminal text", () => {
    expect(parseTerminalControlFrame("$ npm run build\r\n")).toBeNull();
    expect(parseTerminalControlFrame("")).toBeNull();
  });

  it("returns null for JSON-ish PTY output that only looks like a frame", () => {
    // A program printing a brace-wrapped line, and a truncated object: both
    // must reach xterm rather than being swallowed as control messages.
    expect(parseTerminalControlFrame("{ type: attach }")).toBeNull();
    expect(
      parseTerminalControlFrame('{"type":"attach","reattached":fal'),
    ).toBeNull();
  });

  it("returns null for a JSON array or scalar", () => {
    expect(parseTerminalControlFrame('[{"type":"attach"}]')).toBeNull();
    expect(parseTerminalControlFrame('"attach"')).toBeNull();
  });

  it("drops empty replacement fields instead of surfacing empty strings", () => {
    const raw = JSON.stringify({
      type: "attach",
      reattached: false,
      replaced: false,
      replaced_at: "",
      replaced_reason: "",
    });

    expect(parseTerminalControlFrame(raw)).toEqual({
      type: "attach",
      reattached: false,
      replaced: false,
    });
  });
});
