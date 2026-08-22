import { describe, expect, it } from "vitest";

import {
  ClientFrameKind,
  decodeServerFrame,
  encodeClientFrame,
  encodeFocus,
  encodeInput,
  encodeResizeRequest,
} from "../terminalProtocol";
import type { ProtocolError } from "../terminalProtocol";
import {
  CLIENT_FRAME_VECTORS,
  GENERATION_HEX,
  SERVER_FRAME_VECTORS,
} from "./terminalProtocolVectors";

function fromHex(hex: string): Uint8Array {
  const pairs = hex.match(/../g) ?? [];
  return Uint8Array.from(pairs, (pair) => Number.parseInt(pair, 16));
}

function toHex(buffer: ArrayBuffer): string {
  return Array.from(new Uint8Array(buffer), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
}

function bufferFromHex(hex: string): ArrayBuffer {
  return fromHex(hex).buffer;
}

describe("terminalProtocol", () => {
  const generation = fromHex(GENERATION_HEX);

  it("decodes the explicit initial_state vector", () => {
    expect(
      decodeServerFrame(bufferFromHex(SERVER_FRAME_VECTORS.initialState)),
    ).toEqual({
      kind: "initial_state",
      generation,
      sequence: 0x0102030405060708n,
      cols: 80,
      rows: 24,
      retainedLines: 42,
      encoding: "xterm-vt/1",
      data: fromHex("1b5b33316d6869"),
    });
  });

  it("decodes the explicit output vector", () => {
    expect(
      decodeServerFrame(bufferFromHex(SERVER_FRAME_VECTORS.output)),
    ).toEqual({
      kind: "output",
      generation,
      sequence: 9n,
      data: fromHex("68690a"),
    });
  });

  it("decodes the explicit resize vector", () => {
    expect(
      decodeServerFrame(bufferFromHex(SERVER_FRAME_VECTORS.resize)),
    ).toEqual({
      kind: "resize",
      generation,
      sequence: 10n,
      cols: 120,
      rows: 40,
    });
  });

  it("decodes the explicit notice vector", () => {
    expect(
      decodeServerFrame(bufferFromHex(SERVER_FRAME_VECTORS.notice)),
    ).toEqual({
      kind: "notice",
      generation,
      sequence: 11n,
      code: "input_dropped",
      message: "Input dropped",
    });
  });

  it("decodes the explicit close vector", () => {
    expect(
      decodeServerFrame(bufferFromHex(SERVER_FRAME_VECTORS.close)),
    ).toEqual({
      kind: "close",
      generation,
      sequence: 12n,
      reason: "exited",
    });
  });

  it("decodes a frame from a non-zero-byte-offset subarray", () => {
    const frame = fromHex(SERVER_FRAME_VECTORS.output);
    const backing = new Uint8Array(frame.byteLength + 3);
    backing.set(frame, 3);
    expect(decodeServerFrame(backing.subarray(3))).toEqual(
      decodeServerFrame(frame),
    );
  });

  it("encodes every client kind with a zero sequence", () => {
    expect(toHex(encodeInput(generation, "ls\n"))).toBe(
      CLIENT_FRAME_VECTORS.input,
    );
    expect(toHex(encodeResizeRequest(generation, 120, 40))).toBe(
      CLIENT_FRAME_VECTORS.resizeRequest,
    );
    expect(toHex(encodeFocus(generation))).toBe(CLIENT_FRAME_VECTORS.focus);
    expect(
      toHex(
        encodeClientFrame(ClientFrameKind.Input, generation, fromHex("6c730a")),
      ),
    ).toBe(CLIENT_FRAME_VECTORS.input);
  });

  it.each([
    ["short_frame", "4c5401"],
    ["bad_magic", SERVER_FRAME_VECTORS.output.replace(/^4c54/, "0000")],
    ["bad_version", SERVER_FRAME_VECTORS.output.replace(/^4c5401/, "4c5402")],
  ] as const)(
    "throws ProtocolError(%s) for a malformed envelope",
    (code, hex) => {
      expect(() => decodeServerFrame(bufferFromHex(hex))).toThrow(
        expect.objectContaining<Partial<ProtocolError>>({ code }),
      );
    },
  );

  it("rejects truncated kind-specific payloads", () => {
    const shortInitial = SERVER_FRAME_VECTORS.initialState.slice(
      0,
      28 * 2 + 8 * 2,
    );
    const shortResize = SERVER_FRAME_VECTORS.resize.slice(0, -2);
    for (const hex of [shortInitial, shortResize]) {
      expect(() => decodeServerFrame(bufferFromHex(hex))).toThrow(
        expect.objectContaining<Partial<ProtocolError>>({
          code: "short_frame",
        }),
      );
    }
  });

  it("rejects unknown server kinds and malformed notice JSON", () => {
    const unknown = SERVER_FRAME_VECTORS.output.replace(
      /^4c540102/,
      "4c54017f",
    );
    const invalidNotice =
      "4c540104000102030405060708090a0b0c0d0e0f00000000000000017b";
    expect(() => decodeServerFrame(bufferFromHex(unknown))).toThrow(
      expect.objectContaining<Partial<ProtocolError>>({ code: "unknown_kind" }),
    );
    expect(() => decodeServerFrame(bufferFromHex(invalidNotice))).toThrow(
      expect.objectContaining<Partial<ProtocolError>>({
        code: "malformed_payload",
      }),
    );
  });
});
