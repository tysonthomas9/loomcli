import { AppConfigError } from "@/api/appConfig";

import styles from "./BootError.module.css";

export interface BootErrorProps {
  error: unknown;
  onRetry: () => void | Promise<void>;
}

function getErrorMessage(error: unknown): string {
  if (error instanceof AppConfigError) return error.message;
  if (error instanceof Error) return error.message;
  return "An unexpected error occurred";
}

export function BootError({ error, onRetry }: BootErrorProps): JSX.Element {
  return (
    <div className={styles.overlay} role="alert">
      <div className={styles.content}>
        <h2 className={styles.title}>Unable to start application</h2>
        <p className={styles.errorDetail}>{getErrorMessage(error)}</p>
        <button className={styles.retryButton} onClick={() => { void onRetry(); }} type="button">
          Retry
        </button>
      </div>
    </div>
  );
}
