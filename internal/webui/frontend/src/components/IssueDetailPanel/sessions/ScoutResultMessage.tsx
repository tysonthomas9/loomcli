import { MarkdownRenderer } from "../sections/MarkdownRenderer";
import styles from "./SessionsTab.module.css";

interface ScoutRecommendation {
  title: string;
  description: string;
  rationale: string;
  repo: string;
  labels: string[];
  priority: number;
  anchors: string[];
}

interface ScoutSkippedCandidate {
  title: string;
  reason: string;
}

interface ScoutResult {
  recommendations: ScoutRecommendation[];
  skipped: ScoutSkippedCandidate[];
  agentsMd: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function isStringArray(value: unknown): value is string[] {
  return (
    Array.isArray(value) && value.every((item) => typeof item === "string")
  );
}

function isRecommendation(value: unknown): value is ScoutRecommendation {
  if (!isRecord(value)) return false;
  return (
    typeof value.title === "string" &&
    typeof value.description === "string" &&
    typeof value.rationale === "string" &&
    typeof value.repo === "string" &&
    isStringArray(value.labels) &&
    typeof value.priority === "number" &&
    Number.isInteger(value.priority) &&
    isStringArray(value.anchors)
  );
}

function isSkippedCandidate(value: unknown): value is ScoutSkippedCandidate {
  return (
    isRecord(value) &&
    typeof value.title === "string" &&
    typeof value.reason === "string"
  );
}

export function parseScoutResult(content: string): ScoutResult | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(content);
  } catch {
    return null;
  }
  if (!isRecord(parsed)) return null;
  if (
    !Array.isArray(parsed.recommendations) ||
    !parsed.recommendations.every(isRecommendation) ||
    !Array.isArray(parsed.skipped) ||
    !parsed.skipped.every(isSkippedCandidate) ||
    typeof parsed.agentsMd !== "string"
  ) {
    return null;
  }
  return {
    recommendations: parsed.recommendations,
    skipped: parsed.skipped,
    agentsMd: parsed.agentsMd,
  };
}

interface ScoutResultMessageProps {
  content: string;
}

export function ScoutResultMessage({
  content,
}: ScoutResultMessageProps): JSX.Element | null {
  const result = parseScoutResult(content);
  if (!result) return null;

  return (
    <section className={styles.scoutResult} data-testid="scout-result">
      <div className={styles.scoutResultHeader}>
        <strong>Scout result</strong>
        <span>
          {result.recommendations.length} recommended · {result.skipped.length}{" "}
          skipped
        </span>
      </div>

      {result.recommendations.length > 0 ? (
        <div className={styles.recommendationList}>
          {result.recommendations.map((recommendation, index) => (
            <article
              className={styles.recommendationCard}
              key={`${recommendation.repo}-${recommendation.title}-${index}`}
            >
              <div className={styles.recommendationHeading}>
                <h3>{recommendation.title}</h3>
                <div className={styles.recommendationMeta}>
                  <span>{recommendation.repo}</span>
                  <span>P{recommendation.priority}</span>
                </div>
              </div>
              {recommendation.rationale.trim() ? (
                <p className={styles.recommendationRationale}>
                  {recommendation.rationale}
                </p>
              ) : null}
              <MarkdownRenderer
                content={recommendation.description}
                className={styles.recommendationDescription}
              />
              {recommendation.labels.length > 0 ? (
                <div className={styles.recommendationTags} aria-label="Labels">
                  {recommendation.labels.map((label) => (
                    <span key={label}>{label}</span>
                  ))}
                </div>
              ) : null}
              {recommendation.anchors.length > 0 ? (
                <div className={styles.recommendationAnchors}>
                  <strong>Grounded in</strong>
                  {recommendation.anchors.map((anchor) => (
                    <code key={anchor}>{anchor}</code>
                  ))}
                </div>
              ) : null}
            </article>
          ))}
        </div>
      ) : (
        <p className={styles.scoutEmpty}>No new recommendations.</p>
      )}

      {result.skipped.length > 0 ? (
        <details className={styles.scoutDetails}>
          <summary>Skipped candidates ({result.skipped.length})</summary>
          <ul>
            {result.skipped.map((candidate, index) => (
              <li key={`${candidate.title}-${index}`}>
                <strong>{candidate.title}</strong> — {candidate.reason}
              </li>
            ))}
          </ul>
        </details>
      ) : null}

      {result.agentsMd.trim() ? (
        <details className={styles.scoutDetails}>
          <summary>Workspace notes</summary>
          <MarkdownRenderer content={result.agentsMd} />
        </details>
      ) : null}

      <details className={styles.scoutDetails}>
        <summary>Raw result</summary>
        <pre>{JSON.stringify(result, null, 2)}</pre>
      </details>
    </section>
  );
}
