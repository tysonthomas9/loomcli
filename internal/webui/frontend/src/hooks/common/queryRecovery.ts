import { createContext } from "react";

type Refresh = (signal: AbortSignal) => Promise<void>;
type Participant = { name: string; refresh: Refresh; revision: () => number };
type Attempt = {
  promise: Promise<void>;
  resolve: () => void;
  reject: (error: unknown) => void;
  pending: Map<Participant, AbortController>;
  completed: Map<Participant, number>;
};

/** Coordinates successful refreshes of registered surfaces only. This does not
 * certify a server snapshot or authorize advancing an SSE resume checkpoint. */
export class QueryRecoveryCoordinator {
  constructor(readonly scope = "") {}

  private readonly participants = new Set<Participant>();
  private attempt: Attempt | null = null;

  register(
    name: string,
    refresh: Refresh,
    revision: () => number = () => 0,
  ): () => void {
    const participant = { name, refresh, revision };
    this.participants.add(participant);
    if (this.attempt) this.start(this.attempt, participant);
    return () => {
      if (!this.participants.delete(participant)) return;
      const attempt = this.attempt;
      const controller = attempt?.pending.get(participant);
      attempt?.pending.delete(participant);
      attempt?.completed.delete(participant);
      controller?.abort();
      if (attempt) this.maybeComplete(attempt);
    };
  }

  /** Repeated transport failures join ongoing recovery rather than starving it. */
  refresh(): Promise<void> {
    if (this.attempt) return this.attempt.promise;
    let resolve!: () => void;
    let reject!: (error: unknown) => void;
    const promise = new Promise<void>((yes, no) => {
      resolve = yes;
      reject = no;
    });
    const attempt: Attempt = {
      promise,
      resolve,
      reject,
      pending: new Map(),
      completed: new Map(),
    };
    this.attempt = attempt;
    for (const participant of this.participants)
      this.start(attempt, participant);
    this.maybeComplete(attempt);
    return promise;
  }

  /** Scope/owner changes invalidate every completion from the previous attempt. */
  cancel(): void {
    const attempt = this.attempt;
    if (!attempt) return;
    this.fail(
      attempt,
      new DOMException("Query recovery scope changed", "AbortError"),
    );
  }

  private start(attempt: Attempt, participant: Participant): void {
    const revision = participant.revision();
    const controller = new AbortController();
    attempt.pending.set(participant, controller);
    // Defer invocation so synchronous enrollment finishes before requests start.
    void Promise.resolve()
      .then(() => {
        if (this.attempt !== attempt || !attempt.pending.has(participant))
          return;
        return participant.refresh(controller.signal);
      })
      .then(
        () => {
          if (this.attempt !== attempt || !attempt.pending.has(participant))
            return;
          attempt.pending.delete(participant);
          attempt.completed.set(participant, revision);
          this.maybeComplete(attempt);
        },
        (error: unknown) => {
          if (this.attempt !== attempt || !attempt.pending.has(participant))
            return;
          this.fail(
            attempt,
            error instanceof Error
              ? error
              : new Error(`${participant.name}: ${String(error)}`),
          );
        },
      );
  }

  private maybeComplete(attempt: Attempt): void {
    queueMicrotask(() => {
      if (this.attempt !== attempt || attempt.pending.size !== 0) return;
      // An aggregate may finish before another surface. Membership changes
      // during that interval must join the outer barrier before it can finish.
      for (const participant of this.participants) {
        if (attempt.completed.get(participant) !== participant.revision()) {
          this.start(attempt, participant);
        }
      }
      if (attempt.pending.size > 0) return;
      this.attempt = null;
      attempt.resolve();
    });
  }

  private fail(attempt: Attempt, error: unknown): void {
    if (this.attempt !== attempt) return;
    this.attempt = null;
    for (const controller of attempt.pending.values()) controller.abort();
    attempt.pending.clear();
    attempt.reject(error);
  }
}

export const QueryRecoveryContext =
  createContext<QueryRecoveryCoordinator | null>(null);
