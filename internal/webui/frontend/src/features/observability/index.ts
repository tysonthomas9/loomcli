/**
 * Public entry for the Observability frontend feature.
 *
 * Application routing may import this module. Consumers must not reach into
 * feature internals; API mapping, polling state, and presentation stay owned
 * by this vertical slice.
 */

export { ObservabilityPage } from "./ObservabilityPage";
export type {
  HourlyBucket,
  MetricsSnapshot,
  ObservabilityMetricsResponse,
} from "./types";
