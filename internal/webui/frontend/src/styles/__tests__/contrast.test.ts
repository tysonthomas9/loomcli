/**
 * WCAG AA contrast enforcement for the design tokens in variables.css.
 *
 * variables.css is parsed from disk rather than transcribed here, so the
 * registry cannot drift from the stylesheet: adding a surface token
 * automatically adds it to the matrix below, and retuning a text token is
 * checked against every surface in the same theme.
 */

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, it, expect } from "vitest";

import {
  contrastRatio,
  relativeLuminance,
  parseCssVariables,
  deriveSurfaceTokens,
  isOpaqueHex,
  TEXT_TOKENS,
  NON_TEXT_TOKENS,
  NON_TEXT_FLOORS,
  worstRatioAgainstSurfaces,
  WCAG_AA_TEXT,
  WCAG_AA_NON_TEXT,
  type TokenMap,
} from "../contrast";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "..",
  "variables.css",
);
const themes = parseCssVariables(readFileSync(cssPath, "utf8"));
const THEME_NAMES = ["dark", "light"] as const;

function surfaces(theme: TokenMap): string[] {
  return deriveSurfaceTokens(theme);
}

describe("contrastRatio", () => {
  it("returns 21 for black on white", () => {
    expect(contrastRatio("#ffffff", "#000000")).toBeCloseTo(21, 2);
  });

  it("is symmetric", () => {
    expect(contrastRatio("#000000", "#ffffff")).toBeCloseTo(21, 2);
  });

  it("returns 1 for a colour against itself", () => {
    expect(contrastRatio("#7a7a7a", "#7a7a7a")).toBeCloseTo(1, 10);
    expect(contrastRatio("#ffffff", "#ffffff")).toBeCloseTo(1, 10);
  });

  it("expands 3-digit hex the same as 6-digit", () => {
    expect(contrastRatio("#fff", "#000")).toBeCloseTo(21, 2);
  });

  it("rejects non-opaque values", () => {
    expect(() => contrastRatio("rgba(0,0,0,0.5)", "#ffffff")).toThrow();
  });

  it("thresholds match WCAG 2.1 AA", () => {
    expect(WCAG_AA_TEXT).toBe(4.5);
    expect(WCAG_AA_NON_TEXT).toBe(3);
  });
});

describe("relativeLuminance", () => {
  it("anchors at the sRGB extremes", () => {
    expect(relativeLuminance("#000000")).toBeCloseTo(0, 10);
    expect(relativeLuminance("#ffffff")).toBeCloseTo(1, 10);
  });
});

describe("parseCssVariables", () => {
  it("produces a non-empty map for each theme", () => {
    expect(Object.keys(themes.dark).length).toBeGreaterThan(50);
    expect(Object.keys(themes.light).length).toBeGreaterThan(50);
  });

  it("layers the light overrides over the dark base", () => {
    // Only defined in :root, so light inherits it.
    expect(themes.light["--color-priority-0"]).toBe(
      themes.dark["--color-priority-0"],
    );
    // Overridden in [data-theme="light"].
    expect(themes.light["--bg-color"]).not.toBe(themes.dark["--bg-color"]);
  });

  it("resolves var() aliases transitively, per theme", () => {
    expect(themes.dark["--color-text-secondary"]).toBe(
      themes.dark["--text-secondary"],
    );
    expect(themes.light["--color-text-secondary"]).toBe(
      themes.light["--text-secondary"],
    );
    // --color-bg-subtle -> --color-surface-subtle -> literal
    expect(isOpaqueHex(themes.light["--color-bg-subtle"])).toBe(true);
  });

  it("throws on an unresolvable var() reference instead of dropping it", () => {
    expect(() =>
      parseCssVariables(
        ':root { --a: var(--nope); }\n[data-theme="light"] { --a: #fff; }',
      ),
    ).toThrow(/unresolvable/);
  });

  it("throws on a cyclic var() reference", () => {
    expect(() =>
      parseCssVariables(
        ':root { --a: var(--b); --b: var(--a); }\n[data-theme="light"] {}',
      ),
    ).toThrow(/cyclic/);
  });

  it.each(THEME_NAMES)("%s: every registry token is an opaque hex", (name) => {
    const theme = themes[name];
    const bad = [...TEXT_TOKENS, ...surfaces(theme)].filter(
      (token) => !isOpaqueHex(theme[token] ?? ""),
    );
    expect(bad, `tokens that did not resolve to an opaque hex`).toEqual([]);
  });

  it.each(THEME_NAMES)("%s: the surface set is non-trivial", (name) => {
    expect(surfaces(themes[name]).length).toBeGreaterThan(5);
  });
});

describe("WCAG AA: text tokens against every surface token", () => {
  it.each(THEME_NAMES)("%s theme", (name) => {
    const theme = themes[name];
    const failures: string[] = [];

    for (const text of TEXT_TOKENS) {
      for (const surface of surfaces(theme)) {
        const fg = theme[text];
        const bg = theme[surface];
        const ratio = contrastRatio(fg, bg);
        if (ratio < WCAG_AA_TEXT) {
          failures.push(
            `${text} (${fg}) on ${surface} (${bg}): ${ratio.toFixed(2)}:1 < ${WCAG_AA_TEXT}:1`,
          );
        }
      }
    }

    expect(
      failures,
      `${name} theme has ${failures.length} pair(s) below AA:\n  ${failures.join("\n  ")}`,
    ).toEqual([]);
  });
});

describe("non-text tokens: dots, borders and accents", () => {
  it("the text and non-text registries do not overlap", () => {
    const both = NON_TEXT_TOKENS.filter((t) => TEXT_TOKENS.includes(t));
    expect(
      both,
      "a token in both lists would be held to 4.5:1 and flatten the status palette",
    ).toEqual([]);
  });

  it.each(THEME_NAMES)("%s: every non-text token is registered", (name) => {
    expect(Object.keys(NON_TEXT_FLOORS[name]).sort()).toEqual(
      [...NON_TEXT_TOKENS].sort(),
    );
  });

  // A ratchet, not a flat threshold: see the NON_TEXT_FLOORS note in
  // contrast.ts. Tokens already at 3:1 must stay there; the ones below it
  // (light-theme vivid semantics, and --color-status-review in dark) must not
  // regress further while their palette fix waits.
  it.each(THEME_NAMES)("%s: no non-text token gets less legible", (name) => {
    const theme = themes[name];
    const failures: string[] = [];

    for (const token of NON_TEXT_TOKENS) {
      const floor = NON_TEXT_FLOORS[name][token];
      const { ratio, surface } = worstRatioAgainstSurfaces(theme, token);
      if (ratio + 0.005 < floor) {
        failures.push(
          `${token} (${theme[token]}) on ${surface}: ${ratio.toFixed(2)}:1, ` +
            `was ${floor.toFixed(2)}:1 — do not darken non-text tokens for text contrast, ` +
            `use the --color-*-text variant at the call site instead`,
        );
      }
    }

    expect(failures, `${name}:\n  ${failures.join("\n  ")}`).toEqual([]);
  });

  it.each(THEME_NAMES)("%s: the tokens already at 3:1 stay there", (name) => {
    const regressed = NON_TEXT_TOKENS.filter((token) => {
      if (NON_TEXT_FLOORS[name][token] < WCAG_AA_NON_TEXT) return false;
      return (
        worstRatioAgainstSurfaces(themes[name], token).ratio <
        WCAG_AA_NON_TEXT - 0.005
      );
    });
    expect(regressed, `${name} tokens that fell below WCAG 1.4.11`).toEqual([]);
  });
});

describe("regression cases from the accessibility crawl", () => {
  const cases: Array<[keyof typeof themes, string, string, number]> = [
    // The id pill on the list page — measured 2.19:1 before the fix.
    ["light", "--color-text-muted", "--color-bg-tertiary", 2.19],
    // The "Unclaimed" swimlane badge — measured 2.49:1 before the fix.
    ["light", "--color-text-muted", "--bg-card", 2.49],
    // Same muted text on a dark card — measured 2.77:1 before the fix.
    ["dark", "--color-text-muted", "--color-bg-card", 2.77],
  ];

  it.each(cases)("%s: %s on %s (was %s:1)", (theme, text, surface) => {
    const fg = themes[theme][text];
    const bg = themes[theme][surface];
    const ratio = contrastRatio(fg, bg);
    expect(
      ratio,
      `${text} (${fg}) on ${surface} (${bg}) is ${ratio.toFixed(2)}:1`,
    ).toBeGreaterThanOrEqual(WCAG_AA_TEXT);
  });
});

describe("neutral text hierarchy", () => {
  // Contrast against the theme's primary surface must decrease as the token
  // gets more de-emphasised, or the hierarchy has inverted and "muted" text
  // reads louder than body text.
  it.each(THEME_NAMES)("%s: secondary > tertiary > muted", (name) => {
    const theme = themes[name];
    const base = theme["--bg-color"];
    const secondary = contrastRatio(theme["--text-secondary"], base);
    const tertiary = contrastRatio(theme["--color-text-tertiary"], base);
    const muted = contrastRatio(theme["--color-text-muted"], base);

    expect(
      secondary,
      `secondary ${secondary.toFixed(2)} vs tertiary ${tertiary.toFixed(2)}`,
    ).toBeGreaterThan(tertiary);
    expect(
      tertiary,
      `tertiary ${tertiary.toFixed(2)} vs muted ${muted.toFixed(2)}`,
    ).toBeGreaterThan(muted);
  });

  it.each(THEME_NAMES)("%s: primary is the strongest", (name) => {
    const theme = themes[name];
    const base = theme["--bg-color"];
    expect(contrastRatio(theme["--text-primary"], base)).toBeGreaterThan(
      contrastRatio(theme["--text-secondary"], base),
    );
  });
});
