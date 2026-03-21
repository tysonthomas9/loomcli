/**
 * WelcomeBanner component.
 * Per-tab inline welcome overlay explaining the backend and showing example prompts.
 * Auto-dismisses on first non-modifier keypress or manual close.
 */

import { useCallback, useEffect, useRef } from "react";

import styles from "./WelcomeBanner.module.css";

const BACKEND_INFO: Record<
  string,
  { description: string; examples: string[] }
> = {
  claude: {
    description:
      "Claude is an AI assistant by Anthropic. Ask about your codebase, get help with bugs, or discuss architecture.",
    examples: [
      "Explain the architecture of this project",
      "Find potential bugs in the auth module",
      "Help me write tests for the API endpoints",
    ],
  },
  codex: {
    description:
      "Codex is OpenAI\u2019s code-focused model. Great for code generation, refactoring, and technical explanations.",
    examples: [
      "Refactor this function to use async/await",
      "Generate a REST API for user management",
      "Convert this class to use the builder pattern",
    ],
  },
  opencode: {
    description:
      "OpenCode is an open-source coding assistant. Use it for code review, documentation, and pair programming.",
    examples: [
      "Review this pull request for issues",
      "Add JSDoc comments to these functions",
      "Suggest improvements for this algorithm",
    ],
  },
};

const FALLBACK_INFO = {
  description:
    "An AI coding assistant. Ask questions about your codebase, get help debugging, or request code changes.",
  examples: [
    "Explain what this file does",
    "Help me debug this error",
    "Write a unit test for this function",
  ],
};

interface WelcomeBannerProps {
  backendName: string;
  isActive: boolean;
  onDismiss: () => void;
  onExampleClick: (text: string) => void;
}

export function WelcomeBanner({
  backendName,
  isActive,
  onDismiss,
  onExampleClick,
}: WelcomeBannerProps): JSX.Element {
  const dismissedRef = useRef(false);

  const handleDismiss = useCallback(() => {
    if (dismissedRef.current) return;
    dismissedRef.current = true;
    onDismiss();
  }, [onDismiss]);

  useEffect(() => {
    if (!isActive) return;
    const handler = (e: KeyboardEvent) => {
      // Ignore modifier-only presses
      if (
        e.key === "Shift" ||
        e.key === "Control" ||
        e.key === "Alt" ||
        e.key === "Meta"
      )
        return;
      handleDismiss();
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [handleDismiss, isActive]);

  const info = BACKEND_INFO[backendName] ?? FALLBACK_INFO;
  const displayName =
    backendName.charAt(0).toUpperCase() + backendName.slice(1);

  return (
    <div
      className={styles.overlay}
      data-testid="welcome-banner"
      role="region"
      aria-label={`Welcome to ${displayName}`}
    >
      <div className={styles.card}>
        <button
          type="button"
          className={styles.closeButton}
          onClick={handleDismiss}
          aria-label="Dismiss welcome banner"
          data-testid="welcome-banner-close"
        >
          &times;
        </button>
        <h3 className={styles.heading}>Welcome to {displayName}</h3>
        <p className={styles.description}>{info.description}</p>
        <div className={styles.examplesLabel}>Try asking</div>
        <div className={styles.examples}>
          {info.examples.map((example) => (
            <button
              key={example}
              type="button"
              className={styles.example}
              onClick={() => {
                onExampleClick(example);
              }}
              data-testid="welcome-example"
            >
              {example}
            </button>
          ))}
        </div>
        <div className={styles.hint}>
          Press any key or click &times; to dismiss
        </div>
      </div>
    </div>
  );
}
