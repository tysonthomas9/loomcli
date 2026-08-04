/**
 * Unit tests for the check-component-boundaries.mjs AST-based linter.
 *
 * Tests the exported scanFile, scanAll, and ALLOWLIST directly,
 * verifying that cross-component internal imports are detected while
 * barrel imports, self-imports, and non-component imports are allowed.
 */

import { join } from "path";
import { fileURLToPath } from "url";
import { describe, it, expect } from "vitest";

import {
  scanFile,
  scanAll,
  ALLOWLIST,
} from "../check-component-boundaries.mjs";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Resolve the frontend root directory (scripts/../). */
const FRONTEND_ROOT = fileURLToPath(
  new URL("../../", import.meta.url),
);

// ---------------------------------------------------------------------------
// scanFile — cross-component internal imports (violations)
// ---------------------------------------------------------------------------

describe("scanFile — cross-component internal imports", () => {
  it("detects cross-component internal imports", () => {
    const source = `import { foo } from "@/components/Bar/types";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(1);
    expect(violations[0]).toMatchObject({
      relPath: "src/components/Foo/Foo.tsx",
      sourceComponent: "Foo",
      targetComponent: "Bar",
      targetModule: "types",
      importPath: "@/components/Bar/types",
    });
  });

  it("type-only imports are violations", () => {
    const source = `import type { Foo } from "@/components/Bar/types";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(1);
    expect(violations[0]).toMatchObject({
      targetComponent: "Bar",
      targetModule: "types",
    });
  });

  it("re-exports are violations", () => {
    const source = `export { Bar } from "@/components/Baz/internal";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/index.ts"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(1);
    expect(violations[0]).toMatchObject({
      targetComponent: "Baz",
      targetModule: "internal",
      importPath: "@/components/Baz/internal",
    });
  });

  it("detects multiple violations in one file", () => {
    const source = [
      `import { foo } from "@/components/Bar/types";`,
      `import { baz } from "@/components/Qux/helpers";`,
    ].join("\n");
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(2);
    expect(violations[0].targetComponent).toBe("Bar");
    expect(violations[1].targetComponent).toBe("Qux");
  });
});

// ---------------------------------------------------------------------------
// scanFile — allowed imports (no violations)
// ---------------------------------------------------------------------------

describe("scanFile — allowed imports", () => {
  it("allows barrel imports", () => {
    const source = `import { Bar } from "@/components/Bar";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });

  it("allows explicit /index imports", () => {
    const source = `import { Bar } from "@/components/Bar/index";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });

  it("self-imports are fine", () => {
    const source = `import { foo } from "@/components/KanbanBoard/types";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/KanbanBoard/KanbanBoard.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });

  it("non-component @/ imports are ignored", () => {
    const source = `import { foo } from "@/hooks/useSort";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------
// scanFile — relative cross-component imports
// ---------------------------------------------------------------------------

describe("scanFile — relative imports", () => {
  it("detects relative cross-component internal imports", () => {
    const source = `import { formatStatusLabel } from "../StatusColumn/utils";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/IssueDetailPanel/IssueHeader.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(1);
    expect(violations[0]).toMatchObject({
      targetComponent: "StatusColumn",
      targetModule: "utils",
      importPath: "../StatusColumn/utils",
    });
  });

  it("allows relative barrel imports to sibling components", () => {
    const source = `import { IssueCard } from "../IssueCard";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/DraggableIssueCard/DraggableIssueCard.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });

  it("allows relative /index imports to sibling components", () => {
    const source = `import { Bar } from "../Bar/index";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });

  it("ignores relative imports going above components/", () => {
    const source = `import { useSort } from "../../hooks/useSort";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });

  it("allows same-directory relative imports", () => {
    const source = `import { foo } from "./types";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/KanbanBoard/KanbanBoard.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------
// scanFile — dynamic imports
// ---------------------------------------------------------------------------

describe("scanFile — dynamic imports", () => {
  it("detects dynamic import() violations", () => {
    const source = `function load() { return import("@/components/Bar/types"); }`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(1);
    expect(violations[0]).toMatchObject({
      targetComponent: "Bar",
      targetModule: "types",
    });
  });

  it("allows dynamic import() of barrel exports", () => {
    const source = `function load() { return import("@/components/Bar"); }`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------
// scanFile — files outside components/
// ---------------------------------------------------------------------------

describe("scanFile — files outside components/", () => {
  it("files outside components/ are ignored", () => {
    const source = `import { foo } from "@/components/Bar/types";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/hooks/useCustom.ts"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------
// scanFile — line numbers
// ---------------------------------------------------------------------------

describe("scanFile — line numbers", () => {
  it("reports correct line numbers", () => {
    const source = [
      `import React from "react";`,
      `import { useEffect } from "react";`,
      ``,
      `import { foo } from "@/components/Bar/types";`,
      ``,
      `export function Foo() { return null; }`,
    ].join("\n");
    const violations = scanFile(
      join(FRONTEND_ROOT, "src/components/Foo/Foo.tsx"),
      FRONTEND_ROOT,
      source,
    );
    expect(violations).toHaveLength(1);
    expect(violations[0].line).toBe(4);
  });
});

// ---------------------------------------------------------------------------
// scanAll — real codebase integration
// ---------------------------------------------------------------------------

describe("scanAll", () => {
  it("returns 0 violations with real codebase (all known are allowlisted)", () => {
    const result = scanAll(FRONTEND_ROOT);
    expect(result.violations).toHaveLength(0);
    expect(result.allowlistedCount).toBe(ALLOWLIST.length);
    expect(result.scannedCount).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// ALLOWLIST
// ---------------------------------------------------------------------------

describe("ALLOWLIST", () => {
  it("is an array of { source, target } entries", () => {
    expect(Array.isArray(ALLOWLIST)).toBe(true);
    expect(ALLOWLIST.length).toBeGreaterThan(0);
    for (const entry of ALLOWLIST) {
      expect(entry).toHaveProperty("source");
      expect(entry).toHaveProperty("target");
      expect(typeof entry.source).toBe("string");
      expect(typeof entry.target).toBe("string");
    }
  });

  it("known violations produce violations that are properly filtered by allowlist", () => {
    // Pick the first allowlisted entry and verify it would be a violation
    const entry = ALLOWLIST[0];
    // Read the target to construct a synthetic import
    const source = `import { something } from "${entry.target}";`;
    const violations = scanFile(
      join(FRONTEND_ROOT, entry.source),
      FRONTEND_ROOT,
      source,
    );
    // The scanFile function itself does not filter by allowlist —
    // it should report the violation
    expect(violations).toHaveLength(1);
    expect(violations[0].importPath).toBe(entry.target);
  });
});
