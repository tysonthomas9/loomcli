import { useMemo, useState } from "react";

import { ToolPill } from "@/components/ToolPill";
import type { TranscriptEntry } from "@/types/agent";
import { argPreview } from "@/utils/toolPreview";

import { MarkdownRenderer } from "../sections/MarkdownRenderer";
import styles from "./SessionsTab.module.css";

type ToolItem = {
  kind: "tool";
  seq: number;
  name: string;
  input: unknown;
  inputPreview: string;
  result?: string;
  resultTimestamp?: string;
};

type TurnItem = { kind: "text"; seq: number; text: string } | ToolItem;

type RenderBlock =
  | { kind: "interjection"; seq: number; text: string; timestamp?: string }
  | { kind: "turn"; seq: number; timestamp?: string; items: TurnItem[] };

interface GroupedEvents {
  prompt: { text: string; timestamp?: string } | null;
  blocks: RenderBlock[];
}

function formatToolInput(input: unknown): string {
  if (input == null) return "";
  if (typeof input === "string") return input;
  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

function formatTimestamp(ts: string | undefined): string {
  if (!ts) return "";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts;
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  const ss = String(d.getSeconds()).padStart(2, "0");
  const ms = String(d.getMilliseconds()).padStart(3, "0");
  return `${hh}:${mm}:${ss}.${ms}`;
}

function isSyntheticUserContext(text: string): boolean {
  const normalized = text.trimStart();
  return (
    normalized.startsWith("<recommended_plugins>") ||
    normalized.startsWith("# AGENTS.md instructions for ") ||
    normalized.startsWith("<environment_context>") ||
    normalized.startsWith("<INSTRUCTIONS>")
  );
}

export function groupTranscriptEvents(entries: TranscriptEntry[]): GroupedEvents {
  const resultById = new Map<string, TranscriptEntry>();
  for (const entry of entries) {
    if (entry.type === "tool_result" && entry.tool_use_id) {
      resultById.set(entry.tool_use_id, entry);
    }
  }

  let prompt: GroupedEvents["prompt"] = null;
  const blocks: RenderBlock[] = [];
  let current: Extract<RenderBlock, { kind: "turn" }> | null = null;
  let currentUuid: string | undefined;

  const flushCurrent = () => {
    if (current && current.items.length > 0) blocks.push(current);
    current = null;
    currentUuid = undefined;
  };

  for (const entry of entries) {
    if (entry.type === "tool_result") continue;

    if (entry.role === "user" && entry.type === "text") {
      const text = (entry.text ?? "").trim();
      if (!text || isSyntheticUserContext(text)) continue;
      if (!prompt) {
        prompt = entry.timestamp
          ? { text, timestamp: entry.timestamp }
          : { text };
        continue;
      }
      flushCurrent();
      blocks.push(
        entry.timestamp
          ? {
              kind: "interjection",
              seq: entry.seq,
              text,
              timestamp: entry.timestamp,
            }
          : { kind: "interjection", seq: entry.seq, text },
      );
      continue;
    }

    if (entry.role !== "assistant") continue;
    const shouldGroup =
      current && entry.uuid && currentUuid && entry.uuid === currentUuid;
    if (!shouldGroup) {
      flushCurrent();
      current = entry.timestamp
        ? {
            kind: "turn",
            seq: entry.seq,
            timestamp: entry.timestamp,
            items: [],
          }
        : { kind: "turn", seq: entry.seq, items: [] };
      currentUuid = entry.uuid;
    }

    const turn = current!;
    if (entry.type === "text") {
      const text = (entry.text ?? "").trim();
      if (text) turn.items.push({ kind: "text", seq: entry.seq, text });
    } else if (entry.type === "tool_use") {
      const paired = entry.tool_use_id
        ? resultById.get(entry.tool_use_id)
        : undefined;
      const tool: ToolItem = {
        kind: "tool",
        seq: entry.seq,
        name: entry.tool_name ?? "(unknown)",
        input: entry.tool_input,
        inputPreview: formatToolInput(entry.tool_input),
      };
      const resultText = paired?.output || entry.output;
      if (resultText) tool.result = resultText;
      if (paired?.timestamp) tool.resultTimestamp = paired.timestamp;
      turn.items.push(tool);
    }
  }
  flushCurrent();

  return { prompt, blocks };
}

export interface TranscriptWorklogProps {
  entries: TranscriptEntry[];
}

/** Canonical transcript presentation shared by task sessions and run steps. */
export function TranscriptWorklog({
  entries,
}: TranscriptWorklogProps): JSX.Element {
  const [expandedTools, setExpandedTools] = useState<Set<number>>(
    () => new Set(),
  );
  const grouped = useMemo(() => groupTranscriptEvents(entries), [entries]);

  const toggleTool = (seq: number) => {
    setExpandedTools((previous) => {
      const next = new Set(previous);
      if (next.has(seq)) next.delete(seq);
      else next.add(seq);
      return next;
    });
  };

  return (
    <>
      {grouped.prompt ? (
        <details className={styles.promptBlock} open>
          <summary className={styles.promptSummary}>
            <span className={styles.promptLabel}>Prompt</span>
            {grouped.prompt.timestamp ? (
              <span className={styles.ts}>
                {formatTimestamp(grouped.prompt.timestamp)}
              </span>
            ) : null}
          </summary>
          <MarkdownRenderer
            content={grouped.prompt.text}
            className={styles.promptBody}
          />
        </details>
      ) : null}

      {grouped.blocks.map((block) => {
        if (block.kind === "interjection") {
          return (
            <article
              key={`int-${block.seq}`}
              className={styles.interjection}
              data-testid="transcript-interjection"
            >
              <div className={styles.interjectionHeader}>
                <span className={styles.interjectionLabel}>User message</span>
                {block.timestamp ? (
                  <span className={styles.ts}>
                    {formatTimestamp(block.timestamp)}
                  </span>
                ) : null}
              </div>
              <MarkdownRenderer content={block.text} className={styles.msg} />
            </article>
          );
        }
        const toolCount = block.items.filter(
          (item) => item.kind === "tool",
        ).length;
        return (
          <article
            key={`turn-${block.seq}`}
            className={styles.turn}
            data-testid="transcript-event"
            data-role="assistant"
          >
            <div className={styles.turnHeader}>
              {block.timestamp ? (
                <span className={styles.ts}>
                  {formatTimestamp(block.timestamp)}
                </span>
              ) : null}
              {toolCount > 0 ? (
                <span className={styles.turnState}>
                  <span className={styles.turnStateGlyph} />
                  {toolCount === 1 ? "1 tool call" : `${toolCount} tool calls`}
                </span>
              ) : null}
            </div>
            {block.items.map((item) =>
              item.kind === "text" ? (
                <MarkdownRenderer
                  key={item.seq}
                  content={item.text}
                  className={styles.msg}
                />
              ) : (
                <ToolPill
                  key={item.seq}
                  name={item.name}
                  arg={argPreview(item.input)}
                  input={item.inputPreview}
                  result={item.result}
                  resultTimestamp={
                    item.resultTimestamp
                      ? formatTimestamp(item.resultTimestamp)
                      : undefined
                  }
                  expanded={expandedTools.has(item.seq)}
                  onToggle={() => toggleTool(item.seq)}
                  className={styles.toolBlock}
                  testId="transcript-event"
                  dataType="tool_use"
                />
              ),
            )}
          </article>
        );
      })}
    </>
  );
}
