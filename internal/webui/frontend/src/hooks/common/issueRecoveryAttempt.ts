import { readIssueRecovery } from "@/api/common/readIssueRecovery";
import type { PreparedIssueRecovery } from "@/api/common/issueRecovery";
import {
  decodeRecoveryHandle,
  type RecoveryHandle,
} from "@/api/common/recoveryHandle";
export type IssueRecoveryAttemptStatus =
  | "idle"
  | "reading"
  | "prepared"
  | "failed";
export interface IssueRecoveryLease {
  signal: AbortSignal;
  isCurrent(): boolean;
  retry(): boolean;
}
export interface IssueRecoverySelectionLease {
  readonly issueId?: string | undefined;
  readonly signal: AbortSignal;
  isCurrent(): boolean;
  release?(): void;
}
type Attempt = {
  selection?: IssueRecoverySelectionLease;

  lease: IssueRecoveryLease;
  controller: AbortController;
  deadline: number;
  onStatus: (status: IssueRecoveryAttemptStatus) => void;
  detach: () => void;
  timer?: ReturnType<typeof setTimeout>;
  prepared?: PreparedIssueRecovery;
};
/** Preparation only: no snapshot getter, publication or checkpoint acceptance. */
export class IssueRecoveryAttemptController {
  private active: Attempt | undefined;
  private generation = 0;
  private status: IssueRecoveryAttemptStatus = "idle";
  constructor(
    private readonly read: typeof readIssueRecovery = readIssueRecovery,
  ) {}
  getStatus(): IssueRecoveryAttemptStatus {
    return this.status;
  }
  start(
    input: RecoveryHandle,
    lease: IssueRecoveryLease,
    onStatus: (status: IssueRecoveryAttemptStatus) => void,
    selection?: IssueRecoverySelectionLease,
  ): void {
    const capturedSelection =
      selection &&
      Object.freeze({
        issueId: selection.issueId,
        signal: selection.signal,
        isCurrent: () => selection.isCurrent(),
        release: () => selection.release?.(),
      });
    const generation = ++this.generation;
    this.retire();
    if (generation !== this.generation) {
      capturedSelection?.release();
      return;
    }
    const offer = decodeRecoveryHandle(
      input,
      input?.workspace,
      input?.source_repos,
    );
    if (
      lease.signal.aborted ||
      !lease.isCurrent() ||
      (capturedSelection &&
        (capturedSelection.signal.aborted || !capturedSelection.isCurrent()))
    ) {
      capturedSelection?.release();
      return;
    }
    if (generation !== this.generation) {
      capturedSelection?.release();
      return;
    }
    const attempt: Attempt = {
      lease,
      ...(capturedSelection ? { selection: capturedSelection } : {}),
      controller: new AbortController(),
      deadline: offer ? Date.parse(offer.expires_at) : Date.now(),
      onStatus,
      detach: () => {},
    };
    this.active = attempt;
    const canceled = () => {
      if (this.active === attempt) this.cancel();
    };
    lease.signal.addEventListener("abort", canceled, { once: true });
    capturedSelection?.signal.addEventListener("abort", canceled, {
      once: true,
    });
    attempt.detach = () => {
      lease.signal.removeEventListener("abort", canceled);
      capturedSelection?.signal.removeEventListener("abort", canceled);
      capturedSelection?.release();
    };
    if (!offer) {
      this.fail(attempt);
      return;
    }
    this.scheduleExpiry(attempt);
    this.publish(attempt, "reading");
    if (!this.current(attempt)) return;
    void Promise.resolve()
      .then(() => {
        if (!this.current(attempt)) return;
        return this.read(
          offer,
          attempt.controller.signal,
          attempt.selection?.issueId,
        );
      })
      .then(
        (prepared) => {
          if (!this.current(attempt)) return;
          if (!prepared) {
            this.fail(attempt);
            return;
          }
          attempt.prepared = prepared;
          this.publish(attempt, "prepared");
        },
        () => {
          if (this.current(attempt)) this.fail(attempt);
        },
      );
  }
  cancel(): void {
    const generation = ++this.generation;
    const previous = this.active;
    this.retire();
    if (generation !== this.generation) return;
    this.status = "idle";
    if (previous) this.notify(previous, "idle");
  }
  private retire(): void {
    const previous = this.active;
    this.active = undefined;
    this.status = "idle";
    if (!previous) return;
    delete previous.prepared;
    clearTimeout(previous.timer);
    previous.detach();
    previous.controller.abort();
  }
  private owns(attempt: Attempt): boolean {
    if (this.active !== attempt) return false;
    const valid =
      !attempt.lease.signal.aborted &&
      attempt.lease.isCurrent() &&
      (!attempt.selection ||
        (!attempt.selection.signal.aborted && attempt.selection.isCurrent()));
    if (this.active !== attempt) return false;
    if (!valid) {
      this.cancel();
      return false;
    }
    return true;
  }
  private current(attempt: Attempt): boolean {
    if (!this.owns(attempt)) return false;
    if (Date.now() >= attempt.deadline) {
      this.fail(attempt);
      return false;
    }
    return !attempt.controller.signal.aborted;
  }
  private scheduleExpiry(attempt: Attempt): void {
    attempt.timer = setTimeout(
      () => {
        if (!this.current(attempt)) return;
        this.scheduleExpiry(attempt);
      },
      Math.min(Math.max(0, attempt.deadline - Date.now()), 2_147_483_647),
    );
  }
  private fail(attempt: Attempt): void {
    if (this.active !== attempt) return;
    delete attempt.prepared;
    clearTimeout(attempt.timer);
    attempt.controller.abort();
    if (this.active === attempt) this.publish(attempt, "failed");
  }
  private publish(attempt: Attempt, status: IssueRecoveryAttemptStatus): void {
    if (!this.owns(attempt)) return;
    this.status = status;
    this.notify(attempt, status);
    // A status observer can synchronously revoke selection without emitting
    // abort. Retire retained prepared data before returning from publication.
    this.owns(attempt);
  }
  private notify(attempt: Attempt, status: IssueRecoveryAttemptStatus): void {
    try {
      attempt.onStatus(status);
    } catch {
      /* Observers do not own the attempt. */
    }
  }
}
