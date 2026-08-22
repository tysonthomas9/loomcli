const HEADER_BYTES = 28;
const MAGIC = 0x4c54;
const VERSION = 1;
const GENERATION_BYTES = 16;

export const TERMINAL_SUBPROTOCOL = "loom-terminal.v1";

export const ClientFrameKind = {
  Input: 0x81,
  ResizeRequest: 0x82,
  Focus: 0x83,
} as const;

export type ClientFrameKind =
  (typeof ClientFrameKind)[keyof typeof ClientFrameKind];

export type ProtocolErrorCode =
  | "bad_generation"
  | "bad_magic"
  | "bad_version"
  | "malformed_payload"
  | "short_frame"
  | "unknown_kind";

export class ProtocolError extends Error {
  readonly code: ProtocolErrorCode;

  constructor(code: ProtocolErrorCode, message: string) {
    super(message);
    this.name = "ProtocolError";
    this.code = code;
  }
}

interface ServerFrameBase {
  generation: Uint8Array;
  sequence: bigint;
}

export type ServerFrame =
  | (ServerFrameBase & {
      kind: "initial_state";
      cols: number;
      rows: number;
      retainedLines: number;
      encoding: string;
      data: Uint8Array;
    })
  | (ServerFrameBase & { kind: "output"; data: Uint8Array })
  | (ServerFrameBase & { kind: "resize"; cols: number; rows: number })
  | (ServerFrameBase & {
      kind: "notice";
      code: string;
      message: string;
    })
  | (ServerFrameBase & { kind: "close"; reason: string });

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder("utf-8", { fatal: true });

function copyBytes(buffer: ArrayBuffer, offset: number): Uint8Array {
  return new Uint8Array(buffer.slice(offset));
}

function decodeText(bytes: Uint8Array, description: string): string {
  try {
    return textDecoder.decode(bytes);
  } catch {
    throw new ProtocolError(
      "malformed_payload",
      `${description} is not valid UTF-8`,
    );
  }
}

function requirePayloadLength(
  actual: number,
  minimum: number,
  kind: string,
): void {
  if (actual < minimum) {
    throw new ProtocolError(
      "short_frame",
      `${kind} payload requires at least ${minimum} bytes, got ${actual}`,
    );
  }
}

export function encodeClientFrame(
  kind: ClientFrameKind,
  generation: Uint8Array,
  payload: Uint8Array = new Uint8Array(),
): ArrayBuffer {
  if (generation.byteLength !== GENERATION_BYTES) {
    throw new ProtocolError(
      "bad_generation",
      `generation must be ${GENERATION_BYTES} bytes`,
    );
  }

  const frame = new ArrayBuffer(HEADER_BYTES + payload.byteLength);
  const view = new DataView(frame);
  view.setUint16(0, MAGIC, false);
  view.setUint8(2, VERSION);
  view.setUint8(3, kind);
  new Uint8Array(frame, 4, GENERATION_BYTES).set(generation);
  view.setBigUint64(20, 0n, false);
  new Uint8Array(frame, HEADER_BYTES).set(payload);
  return frame;
}

export function encodeResizeRequest(
  generation: Uint8Array,
  cols: number,
  rows: number,
): ArrayBuffer {
  const payload = new Uint8Array(4);
  const view = new DataView(payload.buffer);
  view.setUint16(0, cols, false);
  view.setUint16(2, rows, false);
  return encodeClientFrame(ClientFrameKind.ResizeRequest, generation, payload);
}

export function encodeInput(
  generation: Uint8Array,
  data: string | Uint8Array,
): ArrayBuffer {
  const payload = typeof data === "string" ? textEncoder.encode(data) : data;
  return encodeClientFrame(ClientFrameKind.Input, generation, payload);
}

export function encodeFocus(generation: Uint8Array): ArrayBuffer {
  return encodeClientFrame(ClientFrameKind.Focus, generation);
}

export function decodeServerFrame(buffer: ArrayBuffer): ServerFrame {
  if (buffer.byteLength < HEADER_BYTES) {
    throw new ProtocolError(
      "short_frame",
      `terminal frame requires ${HEADER_BYTES} header bytes`,
    );
  }

  const view = new DataView(buffer);
  const magic = view.getUint16(0, false);
  if (magic !== MAGIC) {
    throw new ProtocolError("bad_magic", "invalid terminal frame magic");
  }
  const version = view.getUint8(2);
  if (version !== VERSION) {
    throw new ProtocolError(
      "bad_version",
      `unsupported terminal protocol version ${version}`,
    );
  }

  const kind = view.getUint8(3);
  const generation = new Uint8Array(buffer.slice(4, 20));
  const sequence = view.getBigUint64(20, false);
  const payloadLength = buffer.byteLength - HEADER_BYTES;

  switch (kind) {
    case 0x01: {
      requirePayloadLength(payloadLength, 9, "initial_state");
      const encodingLength = view.getUint8(36);
      requirePayloadLength(payloadLength, 9 + encodingLength, "initial_state");
      const encodingStart = 37;
      const dataStart = encodingStart + encodingLength;
      return {
        kind: "initial_state",
        generation,
        sequence,
        cols: view.getUint16(28, false),
        rows: view.getUint16(30, false),
        retainedLines: view.getUint32(32, false),
        encoding: decodeText(
          new Uint8Array(buffer, encodingStart, encodingLength),
          "initial_state encoding",
        ),
        data: copyBytes(buffer, dataStart),
      };
    }
    case 0x02:
      return {
        kind: "output",
        generation,
        sequence,
        data: copyBytes(buffer, HEADER_BYTES),
      };
    case 0x03:
      requirePayloadLength(payloadLength, 4, "resize");
      if (payloadLength !== 4) {
        throw new ProtocolError(
          "malformed_payload",
          `resize payload must be 4 bytes, got ${payloadLength}`,
        );
      }
      return {
        kind: "resize",
        generation,
        sequence,
        cols: view.getUint16(28, false),
        rows: view.getUint16(30, false),
      };
    case 0x04: {
      const text = decodeText(
        copyBytes(buffer, HEADER_BYTES),
        "notice payload",
      );
      let value: unknown;
      try {
        value = JSON.parse(text);
      } catch {
        throw new ProtocolError(
          "malformed_payload",
          "notice payload is not valid JSON",
        );
      }
      if (
        typeof value !== "object" ||
        value === null ||
        typeof (value as { code?: unknown }).code !== "string" ||
        typeof (value as { message?: unknown }).message !== "string"
      ) {
        throw new ProtocolError(
          "malformed_payload",
          "notice payload requires string code and message fields",
        );
      }
      return {
        kind: "notice",
        generation,
        sequence,
        code: (value as { code: string }).code,
        message: (value as { message: string }).message,
      };
    }
    case 0x05:
      return {
        kind: "close",
        generation,
        sequence,
        reason: decodeText(copyBytes(buffer, HEADER_BYTES), "close payload"),
      };
    default:
      throw new ProtocolError(
        "unknown_kind",
        `unknown server frame kind 0x${kind.toString(16).padStart(2, "0")}`,
      );
  }
}
