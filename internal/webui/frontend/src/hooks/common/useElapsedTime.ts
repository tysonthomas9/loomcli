/**
 * Hook that returns a formatted elapsed time string from a start timestamp.
 * Updates every second while active.
 */

import { useState, useEffect } from "react";

export function formatElapsed(ms: number): string {
  if (ms <= 0) return "0s";
  const totalSeconds = Math.floor(ms / 1000);
  if (totalSeconds < 60) return `${totalSeconds}s`;
  const totalMinutes = Math.floor(totalSeconds / 60);
  if (totalMinutes < 60) return `${totalMinutes}m`;
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  return `${hours}h ${minutes}m`;
}

export function useElapsedTime(startTimestamp: number | null): string {
  const [elapsed, setElapsed] = useState("");

  useEffect(() => {
    if (startTimestamp === null) {
      setElapsed("");
      return;
    }

    const update = () => {
      const diff = Date.now() - startTimestamp;
      setElapsed(formatElapsed(diff));
    };

    update();
    const interval = setInterval(update, 1000);
    return () => clearInterval(interval);
  }, [startTimestamp]);

  return elapsed;
}
