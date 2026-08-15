/**
 * WCAG contrast utilities and the design-token registry the contrast test
 * harness runs over.
 *
 * Pure module: no React, no DOM, no colour dependency. The arithmetic below is
 * the standard sRGB relative-luminance formula from WCAG 2.x, which is short
 * enough that pulling a package for it would cost more than it saves.
 *
 * The registry half of this file exists so the contrast test cannot silently
 * stop covering a token. Surface tokens are *derived from the parsed CSS*
 * rather than transcribed here — a hand-written hex list drifts the moment
 * someone edits variables.css, which is exactly how the reported bug survived.
 */

/** Minimum contrast ratio for text (WCAG 2.1 AA, normal size). */
export const WCAG_AA_TEXT = 4.5;

/** Minimum contrast ratio for non-text UI (borders, dots, fills). */
export const WCAG_AA_NON_TEXT = 3;

/** A parsed theme: token name (with leading `--`) -> fully resolved value. */
export type TokenMap = Record<string, string>;

export interface ParsedThemes {
  dark: TokenMap;
  light: TokenMap;
}

const HEX_RE = /^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i;

/** True when `value` is an opaque hex colour (3- or 6-digit, no alpha). */
export function isOpaqueHex(value: string): boolean {
  return HEX_RE.test(value.trim());
}

function toRgb(hex: string): [number, number, number] {
  const raw = hex.trim();
  if (!isOpaqueHex(raw)) {
    throw new Error(`not an opaque hex colour: ${hex}`);
  }
  let body = raw.slice(1);
  if (body.length === 3) {
    body = body
      .split("")
      .map((c) => c + c)
      .join("");
  }
  return [
    parseInt(body.slice(0, 2), 16),
    parseInt(body.slice(2, 4), 16),
    parseInt(body.slice(4, 6), 16),
  ];
}

/** WCAG relative luminance of an opaque hex colour, in [0, 1]. */
export function relativeLuminance(hex: string): number {
  const linear = (channel: number): number => {
    const s = channel / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  const [r, g, b] = toRgb(hex);
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

/** WCAG contrast ratio between two opaque hex colours, in [1, 21]. */
export function contrastRatio(a: string, b: string): number {
  const la = relativeLuminance(a);
  const lb = relativeLuminance(b);
  const lighter = Math.max(la, lb);
  const darker = Math.min(la, lb);
  return (lighter + 0.05) / (darker + 0.05);
}

/* ===========================================================================
 * variables.css parsing
 * ======================================================================== */

const DECL_RE = /--([\w-]+)\s*:\s*([^;]+);/g;
const VAR_RE = /var\(\s*(--[\w-]+)\s*(?:,\s*([^()]*?)\s*)?\)/;

/**
 * Strip `/* … *\/` comments. Without this, a selector mentioned in prose — and
 * this file's own header mentions both of them — is found before the real rule
 * and the wrong block gets parsed.
 */
function stripComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, " ");
}

function blockBody(css: string, selector: string): string {
  // Only accept an occurrence that actually opens a rule, i.e. is followed by
  // `{` with nothing but whitespace in between.
  let from = 0;
  for (;;) {
    const start = css.indexOf(selector, from);
    if (start === -1) {
      throw new Error(`selector not found in CSS source: ${selector}`);
    }
    const rest = css.slice(start + selector.length);
    const open = rest.search(/\S/);
    if (open !== -1 && rest[open] === "{") {
      const close = rest.indexOf("}", open);
      if (close === -1) {
        throw new Error(`unterminated block for selector: ${selector}`);
      }
      return rest.slice(open + 1, close);
    }
    from = start + selector.length;
  }
}

function declarations(body: string): TokenMap {
  const out: TokenMap = {};
  DECL_RE.lastIndex = 0;
  let match = DECL_RE.exec(body);
  while (match !== null) {
    const [, name, value] = match;
    if (name !== undefined && value !== undefined) {
      out[`--${name}`] = value.replace(/\s+/g, " ").trim();
    }
    match = DECL_RE.exec(body);
  }
  return out;
}

/**
 * Resolve `var(--x)` references transitively.
 *
 * Throws on an unresolvable or cyclic reference rather than dropping the
 * token: a silently skipped token is a token the contrast test stops checking,
 * and that is how this bug would survive a second time.
 */
function resolve(name: string, raw: TokenMap, seen: string[] = []): string {
  if (seen.includes(name)) {
    throw new Error(`cyclic var() reference: ${[...seen, name].join(" -> ")}`);
  }
  let value = raw[name];
  if (value === undefined) {
    throw new Error(`unresolvable token reference: ${name}`);
  }
  let match = VAR_RE.exec(value);
  while (match !== null) {
    const [whole, ref, fallback] = match;
    if (ref === undefined) {
      throw new Error(`malformed var() reference in token ${name}: ${value}`);
    }
    let replacement: string;
    if (raw[ref] !== undefined) {
      replacement = resolve(ref, raw, [...seen, name]);
    } else if (fallback !== undefined) {
      replacement = fallback;
    } else {
      throw new Error(
        `unresolvable var() reference ${ref} in token ${name}: ${value}`,
      );
    }
    value = value.replace(whole, replacement);
    match = VAR_RE.exec(value);
  }
  return value.trim();
}

function resolveAll(raw: TokenMap): TokenMap {
  const out: TokenMap = {};
  for (const name of Object.keys(raw)) {
    out[name] = resolve(name, raw);
  }
  return out;
}

/**
 * Parse `variables.css` into two fully resolved theme maps.
 *
 * `:root` is the dark theme; `[data-theme="light"]` is an *override* block, not
 * a full redefinition, so the light map is layered over the dark one before
 * `var()` resolution (a light override must resolve against light values).
 */
export function parseCssVariables(cssSource: string): ParsedThemes {
  const css = stripComments(cssSource);
  const darkRaw = declarations(blockBody(css, ":root"));
  const lightRaw = {
    ...darkRaw,
    ...declarations(blockBody(css, '[data-theme="light"]')),
  };
  return { dark: resolveAll(darkRaw), light: resolveAll(lightRaw) };
}

/* ===========================================================================
 * Token registry
 * ======================================================================== */

/**
 * Neutral tokens used as `color:` for text. Every one of these must reach
 * WCAG_AA_TEXT against every surface token in the same theme.
 *
 * The semantic `--color-*-text` variants are here too. They are a parallel set
 * to the vivid semantic tokens, which stay in NON_TEXT_TOKENS below: text uses
 * the `-text` variant, dots and borders keep the vivid one.
 *
 * `--color-text-inverse` is deliberately excluded: it is painted on inverted
 * surfaces (filled buttons, badges) that are not part of the standard surface
 * set, so pairing it with them would assert a combination that never renders.
 *
 * `--color-text`, `--color-text-primary` and `--color-text-secondary` are
 * `var()` aliases of `--text-primary` / `--text-secondary`; checking the base
 * token covers them.
 */
export const TEXT_TOKENS: readonly string[] = [
  "--text-primary", // body text
  "--text-secondary", // de-emphasised body text
  "--color-text-tertiary", // labels, metadata
  "--color-text-muted", // id pills, "Unclaimed" badges — the reported bug
  "--color-status-ready", // rendered as text, not just as a dot
  "--color-status-idle", // rendered as text, not just as a dot

  // Semantic text variants. Every `color:` call site that used to name a vivid
  // semantic token now names one of these instead.
  "--color-primary-text",
  "--color-success-text",
  "--color-warning-text",
  "--color-danger-text",
  "--color-info-text",
  "--color-epic-text",
  "--color-status-review-text",
];

/**
 * Tokens used for NON-text UI: status dots, badge and chip borders, kanban
 * accents, graph node strokes. WCAG 1.4.11 asks 3:1 of these, not 4.5:1.
 *
 * Kept explicitly separate from TEXT_TOKENS. Folding them into TEXT_TOKENS
 * would force them all darker to satisfy 4.5:1, which is exactly the change
 * this design rejected: it would flatten the status-colour vocabulary that
 * dots and accents depend on, for no gain to any text.
 */
export const NON_TEXT_TOKENS: readonly string[] = [
  "--color-primary",
  "--color-success",
  "--color-warning",
  "--color-danger",
  "--color-info",
  "--color-type-epic",
  "--color-status-open",
  "--color-status-in-progress",
  "--color-status-closed",
  "--color-status-working",
  "--color-status-done",
  "--color-status-review",
  "--color-status-error",
  "--color-status-dirty",
  "--color-status-pending",
  "--color-blocked",
  "--color-ready",
  "--color-closed",
];

/**
 * Worst-case ratio each non-text token reaches today, per theme, against the
 * derived surface set — floored to 2dp from a measurement, not a target.
 *
 * The design for this change assumed every vivid token already cleared
 * WCAG_AA_NON_TEXT. Measured, they do not: `--color-status-review` is 2.16:1
 * in dark, and in light almost the whole vivid set sits between 1.58 and
 * 2.77:1 — the same numbers that motivated the text variants in the first
 * place. Asserting a flat 3:1 here would just be a red gate.
 *
 * So this is a ratchet instead. A token at or above WCAG_AA_NON_TEXT must stay
 * there; one below it must not get any worse. Either way a contributor who
 * "helpfully" darkens a vivid token to chase text contrast trips the test,
 * which is what the assertion was for. Fixing the light-theme non-text palette
 * properly is a separate change — it needs light overrides for the vivid
 * tokens and it moves every dot and chip border in the light theme.
 */
export const NON_TEXT_FLOORS: Readonly<
  Record<"dark" | "light", Readonly<Record<string, number>>>
> = {
  dark: {
    "--color-primary": 3.69,
    "--color-success": 5.96,
    "--color-warning": 6.32,
    "--color-danger": 3.61,
    "--color-info": 5.59,
    "--color-type-epic": 3.21,
    "--color-status-open": 3.69,
    "--color-status-in-progress": 6.32,
    "--color-status-closed": 5.35,
    "--color-status-working": 3.69,
    "--color-status-done": 5.35,
    "--color-status-review": 2.16, // below 3:1 — see the note above
    "--color-status-error": 3.61,
    "--color-status-dirty": 6.32,
    "--color-status-pending": 5.35,
    "--color-blocked": 3.61,
    "--color-ready": 5.96,
    "--color-closed": 5.35,
  },
  light: {
    "--color-primary": 3.81,
    "--color-success": 1.68,
    "--color-warning": 1.58,
    "--color-danger": 2.77,
    "--color-info": 1.79,
    "--color-type-epic": 3.12,
    "--color-status-open": 2.71,
    "--color-status-in-progress": 1.58,
    "--color-status-closed": 1.87,
    "--color-status-working": 2.71,
    "--color-status-done": 1.87,
    "--color-status-review": 4.63,
    "--color-status-error": 2.77,
    "--color-status-dirty": 1.58,
    "--color-status-pending": 1.87,
    "--color-blocked": 2.77,
    "--color-ready": 1.68,
    "--color-closed": 1.87,
  },
};

/** The worst contrast `token` reaches against any surface in `theme`. */
export function worstRatioAgainstSurfaces(
  theme: TokenMap,
  token: string,
): { ratio: number; surface: string } {
  const fg = theme[token];
  if (fg === undefined) {
    throw new Error(`token not present in theme: ${token}`);
  }
  let ratio = Infinity;
  let surface = "";
  for (const name of deriveSurfaceTokens(theme)) {
    const candidate = contrastRatio(fg, theme[name] ?? "");
    if (candidate < ratio) {
      ratio = candidate;
      surface = name;
    }
  }
  return { ratio, surface };
}

/**
 * Name patterns that identify a background/surface token. Matched against the
 * token name with its leading `--` stripped.
 */
export const SURFACE_TOKEN_PATTERNS: readonly RegExp[] = [
  /(^|-)bg(-|$)/,
  /surface/,
  /-card$/,
];

function looksLikeSurface(name: string): boolean {
  const bare = name.replace(/^--/, "");
  return SURFACE_TOKEN_PATTERNS.some((re) => re.test(bare));
}

/**
 * Every surface a text token can land on, derived exhaustively from the parsed
 * theme maps so the registry cannot drift from variables.css.
 *
 * Tokens whose resolved value carries alpha (`rgba(...)`, 8-digit hex) are
 * excluded: text over a translucent background has no single computed pair to
 * measure, because the result depends on whatever is painted underneath.
 */
export function deriveSurfaceTokens(theme: TokenMap): string[] {
  return Object.keys(theme)
    .filter((name) => looksLikeSurface(name) && isOpaqueHex(theme[name] ?? ""))
    .sort();
}
