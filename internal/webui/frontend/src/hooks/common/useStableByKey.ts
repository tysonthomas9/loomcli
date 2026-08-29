import { useRef } from "react";

/**
 * Keeps a derived array referentially stable while its key is unchanged.
 *
 * `useMemo` cannot do this job here: the arrays these callers derive are rebuilt
 * whenever any upstream object identity changes, even when the contents are the
 * same, and each one then feeds a data-fetching effect keyed on identity. The
 * key is the value that actually decides whether the array is different — a
 * join of the entries' own keys — so holding the last value for a given key
 * turns "same contents" into "same reference".
 *
 * Written as a ref compare during render rather than an effect, so the stable
 * value is available on the render that produced it and consumers never see one
 * frame of the unstable array.
 */
export function useStableByKey<T>(key: string, value: T): T {
  const held = useRef<{ key: string; value: T } | null>(null);
  if (held.current === null || held.current.key !== key) {
    held.current = { key, value };
  }
  return held.current.value;
}
