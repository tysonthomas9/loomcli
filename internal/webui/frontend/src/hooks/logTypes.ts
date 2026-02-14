/**
 * Shared types for terminal/log rendering surfaces.
 */

/**
 * A single raw terminal/log chunk.
 */
export interface LogChunk {
  chunk: Uint8Array;
  byteOffset: number;
  timestamp: string;
}

/**
 * Connection states used by LogViewer.
 */
export type LogStreamState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting';
