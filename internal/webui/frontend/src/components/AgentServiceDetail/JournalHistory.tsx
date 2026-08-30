import { MarkdownRenderer } from "@/components/IssueDetailPanel";
import { formatFireTime } from "@/utils/bindingDisplay";

import styles from "./AgentServiceDetail.module.css";

interface JournalSection {
  label: string;
  items: string[];
}

export interface JournalRun {
  timestamp: string;
  runId?: string;
  sections: JournalSection[];
}

export interface ParsedJournal {
  title: string;
  introduction: string;
  runs: JournalRun[];
}

const RUN_HEADING = /^##\s+Run\s+(.+?)(?:\s+\(driver run\s+([^)]+)\))?\s*$/;
const SECTION_HEADING = /^([^#\n][^\n]*):\s*$/;

function cleanPreamble(lines: string[]): {
  title: string;
  introduction: string;
} {
  const meaningful = lines.map((line) => line.trim()).filter(Boolean);
  const heading = meaningful.find((line) => line.startsWith("# "));
  return {
    title: heading?.slice(2).trim() || "Journal",
    introduction: meaningful
      .filter((line) => line !== heading)
      .join(" ")
      .trim(),
  };
}

function parseSections(lines: string[]): JournalSection[] {
  const sections: JournalSection[] = [];
  let current: JournalSection | null = null;

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line) continue;
    const heading = SECTION_HEADING.exec(line);
    if (heading) {
      current = { label: heading[1]?.trim() || "Details", items: [] };
      sections.push(current);
      continue;
    }
    if (!current) continue;
    const item = line.replace(/^[-*]\s+/, "").trim();
    if (item && item.toLowerCase() !== "none") current.items.push(item);
  }

  return sections;
}

export function parseJournal(content: string): ParsedJournal | null {
  const lines = content.split(/\r?\n/);
  const runStarts: Array<{
    index: number;
    timestamp: string;
    runId?: string;
  }> = [];

  lines.forEach((line, index) => {
    const match = RUN_HEADING.exec(line.trim());
    if (!match?.[1]) return;
    const start = { index, timestamp: match[1].trim() };
    runStarts.push(match[2] ? { ...start, runId: match[2].trim() } : start);
  });

  if (runStarts.length === 0) return null;

  const { title, introduction } = cleanPreamble(
    lines.slice(0, runStarts[0]?.index ?? 0),
  );
  const runs = runStarts.map((start, position) => {
    const next = runStarts[position + 1];
    const sections = parseSections(
      lines.slice(start.index + 1, next?.index ?? lines.length),
    );
    return start.runId
      ? { timestamp: start.timestamp, runId: start.runId, sections }
      : { timestamp: start.timestamp, sections };
  });

  return { title, introduction, runs };
}

function sectionItems(run: JournalRun, label: string): string[] {
  return (
    run.sections.find(
      (section) => section.label.toLowerCase() === label.toLowerCase(),
    )?.items ?? []
  );
}

function countLabel(
  count: number,
  singular: string,
  plural = singular,
): string {
  return `${count} ${count === 1 ? singular : plural}`;
}

export interface JournalHistoryProps {
  content: string;
}

export function JournalHistory({ content }: JournalHistoryProps): JSX.Element {
  const journal = parseJournal(content);
  if (!journal) {
    return (
      <MarkdownRenderer content={content} className={styles.journalContent} />
    );
  }

  return (
    <div className={styles.journalHistory}>
      {journal.introduction ? (
        <p className={styles.journalIntroduction}>{journal.introduction}</p>
      ) : null}
      <div className={styles.journalRunList}>
        {[...journal.runs].reverse().map((run, index) => {
          const repos = sectionItems(run, "Repos analyzed");
          const created = sectionItems(run, "Issues created");
          const skipped = sectionItems(run, "Skipped candidates");
          const warnings = sectionItems(run, "Warnings");
          const otherSections = run.sections.filter(
            (section) =>
              ![
                "repos analyzed",
                "issues created",
                "skipped candidates",
                "warnings",
              ].includes(section.label.toLowerCase()),
          );
          return (
            <details
              key={run.runId ?? `${run.timestamp}-${index}`}
              className={styles.journalRun}
            >
              <summary className={styles.journalRunSummary}>
                <span className={styles.journalRunTime}>
                  {formatFireTime(run.timestamp) || run.timestamp}
                </span>
                <span className={styles.journalRunMetrics}>
                  <span data-tone={created.length > 0 ? "positive" : undefined}>
                    {countLabel(created.length, "created")}
                  </span>
                  <span>{countLabel(skipped.length, "skipped")}</span>
                  {warnings.length > 0 ? (
                    <span data-tone="warning">
                      {countLabel(warnings.length, "warning", "warnings")}
                    </span>
                  ) : null}
                </span>
                <span className={styles.journalRunChevron} aria-hidden="true">
                  ▸
                </span>
              </summary>
              <div className={styles.journalRunBody}>
                {run.runId ? (
                  <p className={styles.journalRunId}>Driver run {run.runId}</p>
                ) : null}
                {[
                  { label: "Repos analyzed", items: repos },
                  { label: "Issues created", items: created },
                  { label: "Skipped candidates", items: skipped },
                  { label: "Warnings", items: warnings },
                  ...otherSections,
                ]
                  .filter((section) => section.items.length > 0)
                  .map((section) => (
                    <div className={styles.journalSection} key={section.label}>
                      <h3>{section.label}</h3>
                      <ul>
                        {section.items.map((item) => (
                          <li key={item}>{item}</li>
                        ))}
                      </ul>
                    </div>
                  ))}
                {run.sections.every((section) => section.items.length === 0) ? (
                  <p className={styles.emptyText}>No changes recorded.</p>
                ) : null}
              </div>
            </details>
          );
        })}
      </div>
      <details className={styles.fullJournal}>
        <summary>Full journal</summary>
        <MarkdownRenderer content={content} className={styles.journalContent} />
      </details>
    </div>
  );
}
