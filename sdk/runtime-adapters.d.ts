export interface LoomTranscriptEntry {
  seq: number;
  timestamp: string;
  role: "user" | "assistant" | "tool" | "system";
  type: "text" | "tool_use" | "tool_result" | "session_meta";
  text?: string;
  tool_name?: string;
  tool_use_id?: string;
  tool_input?: unknown;
  output?: string;
  uuid?: string;
}

export interface FlueTranscriptCollector {
  entries: LoomTranscriptEntry[];
  push(event: Record<string, unknown>): LoomTranscriptEntry[];
}

export interface FlueTranscriptOptions {
  dedupePrompts?: boolean;
}

export interface FlueUsageOptions {
  costUnit?: string;
}

export interface LoomTaskUsage {
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
  estimated_cost_usd?: number;
}

export declare function createFlueTranscriptCollector(options?: FlueTranscriptOptions): FlueTranscriptCollector;
export declare function flueEventToTranscriptEntries(
  event: Record<string, unknown>,
  state?: Record<string, unknown>,
  options?: FlueTranscriptOptions,
): LoomTranscriptEntry[];
export declare function serializeTranscriptJSONL(entries?: LoomTranscriptEntry[]): string;
export declare function flueUsageToTaskUsage(usage: unknown, options?: FlueUsageOptions): LoomTaskUsage;
export declare function flueEventsToTaskUsage(events?: Array<Record<string, unknown>>, options?: FlueUsageOptions): LoomTaskUsage;
export declare function flueEventsToLogText(events?: Array<Record<string, unknown>>): string;
export declare function redactText(value: unknown, secrets?: string[]): string;
export declare function redactTranscriptEntries(entries?: LoomTranscriptEntry[], secrets?: string[]): LoomTranscriptEntry[];
