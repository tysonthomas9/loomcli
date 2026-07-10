import { KNOWN_BACKEND_DEFAULTS } from "@/utils/workspace";

/** Brand colors for each known terminal backend. */
export const BACKEND_BRAND_COLORS: Record<string, string> = Object.fromEntries(
  Object.entries(KNOWN_BACKEND_DEFAULTS).map(([key, value]) => [
    key,
    value.brandColor,
  ]),
);
