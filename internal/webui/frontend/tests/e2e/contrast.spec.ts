import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E: WCAG AA contrast for the two elements reported as unreadable.
 *
 * The unit harness (src/styles/__tests__/contrast.test.ts) proves the *tokens*
 * in variables.css are compliant. This spec proves the browser actually paints
 * them there: it reads the computed foreground/background off the real DOM and
 * computes the ratio in-page, once per theme.
 *
 * There is no data-testid on any theme toggle, so the theme is driven the same
 * way the app persists it: `data-theme` on <html> plus the localStorage key
 * written by src/hooks/ui/useTheme.ts.
 */

const THEME_STORAGE_KEY = "cortex:theme";

const mockWorkspaceData = {
  id: "default",
  name: "default",
  path: "/tmp/test-ws",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: "default",
      name: "default",
      path: "/tmp/test-ws",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: ["default"],
  default_workspace: "default",
};

/** One unclaimed epic with children, so the list renders a lane + id pills. */
const mockIssues = [
  {
    id: "CONTRAST-1",
    title: "Readable text everywhere",
    status: "open",
    priority: 2,
    issue_type: "epic",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "CONTRAST-2",
    title: "Retune the neutral text tokens",
    status: "open",
    priority: 2,
    issue_type: "task",
    parent: "CONTRAST-1",
    parent_title: "Readable text everywhere",
    created_at: "2026-01-15T10:01:00Z",
    updated_at: "2026-01-15T10:01:00Z",
  },
  {
    id: "CONTRAST-3",
    title: "Add the contrast test harness",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    parent: "CONTRAST-1",
    parent_title: "Readable text everywhere",
    created_at: "2026-01-15T10:02:00Z",
    updated_at: "2026-01-15T10:02:00Z",
  },
];

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

async function installIssuesMock(page: Page, issues: unknown[]) {
  await page.addInitScript((issueData: unknown[]) => {
    (window as unknown as { __mockIssues: unknown[] }).__mockIssues = issueData;
    const originalFetch = window.fetch.bind(window);
    window.fetch = function (
      input: RequestInfo | URL,
      init?: RequestInit,
    ): Promise<Response> {
      const url =
        typeof input === "string"
          ? input
          : input instanceof Request
            ? input.url
            : input.toString();
      if ((init?.method ?? "GET") !== "GET") return originalFetch(input, init);
      if (
        /\/api\/workspaces\/[^/]+\/(issues(\/graph)?|ready)(\?|$)/.test(url)
      ) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              success: true,
              data: (window as unknown as { __mockIssues: unknown[] })
                .__mockIssues,
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      return originalFetch(input, init);
    };
  }, issues);
}

async function setupMocks(page: Page) {
  await page.route("**/api/config", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    }),
  );
  await page.route("**/api/workspaces/active", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok(mockWorkspaceData),
    }),
  );
  await page.route("**/api/workspaces/default", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.replace(/\/$/, "") === "/api/workspaces/default") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockWorkspaceData),
      });
      return;
    }
    await route.fallback();
  });
  await page.route("**/api/health", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    }),
  );
  await page.route("**/api/monitor/**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    }),
  );
  await page.route("**/workspaces/*/blocked*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([]),
    }),
  );
  await page.route("**/workspaces/*/terminal/sessions/by-issue", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({}),
    }),
  );
  await page.route("**/workspaces/*/events**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: { "Cache-Control": "no-cache", Connection: "keep-alive" },
      body: 'event: connected\ndata: {"message":"connected"}\n\n',
    }),
  );
}

/** Pin the theme before any app code runs, then keep it pinned after hydration. */
async function pinTheme(page: Page, theme: "dark" | "light") {
  await page.addInitScript(
    ([key, value]: [string, string]) => {
      try {
        localStorage.setItem(key, value);
      } catch {
        // localStorage unavailable — the data-theme attribute below still wins
      }
      document.documentElement.dataset.theme = value;
    },
    [THEME_STORAGE_KEY, theme] as [string, string],
  );
}

/**
 * Compute the WCAG ratio between an element's own colour and the first
 * non-transparent background painted behind it, using the browser's own
 * computed styles.
 */
async function ratioFor(page: Page, selector: string) {
  return page.evaluate((sel) => {
    const el = document.querySelector(sel);
    if (!el) throw new Error(`element not found: ${sel}`);

    const parse = (value: string): [number, number, number, number] | null => {
      const m = value.match(/rgba?\(([^)]+)\)/);
      if (!m) return null;
      const parts = m[1]
        .split(/[,\s/]+/)
        .filter(Boolean)
        .map(Number);
      return [parts[0], parts[1], parts[2], parts.length > 3 ? parts[3] : 1];
    };

    const luminance = ([r, g, b]: number[]) => {
      const [lr, lg, lb] = [r, g, b].map((c) => {
        const s = c / 255;
        return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
      });
      return 0.2126 * lr + 0.7152 * lg + 0.0722 * lb;
    };

    const fg = parse(getComputedStyle(el).color);
    if (!fg) throw new Error(`unreadable color on ${sel}`);

    let node: Element | null = el;
    let bg: [number, number, number, number] | null = null;
    while (node) {
      const parsed = parse(getComputedStyle(node).backgroundColor);
      if (parsed && parsed[3] > 0) {
        bg = parsed;
        break;
      }
      node = node.parentElement;
    }
    if (!bg) throw new Error(`no opaque background behind ${sel}`);

    const lf = luminance(fg);
    const lb = luminance(bg);
    const ratio = (Math.max(lf, lb) + 0.05) / (Math.min(lf, lb) + 0.05);
    return {
      ratio,
      fg: getComputedStyle(el).color,
      bg: getComputedStyle(node as Element).backgroundColor,
    };
  }, selector);
}

const TARGETS: Array<[string, string]> = [
  ["issue id pill", 'code[class*="rowId"]'],
  ["unclaimed lane badge", '[data-testid="lane-unclaimed-badge"]'],
];

for (const theme of ["dark", "light"] as const) {
  test.describe(`WCAG AA contrast on the list page (${theme} theme)`, () => {
    test.beforeEach(async ({ page }) => {
      await pinTheme(page, theme);
      await installIssuesMock(page, mockIssues);
      await setupMocks(page);
      await page.goto("/ws/default/list", { waitUntil: "domcontentloaded" });
      await expect(
        page.locator('[data-testid="lane-unclaimed-badge"]').first(),
      ).toBeVisible();
      await expect(page.locator('code[class*="rowId"]').first()).toBeVisible();
      // useTheme applies the persisted theme on mount; confirm it stuck.
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    });

    for (const [label, selector] of TARGETS) {
      test(`${label} reaches 4.5:1`, async ({ page }) => {
        const { ratio, fg, bg } = await ratioFor(page, selector);
        expect(
          ratio,
          `${label} (${theme}): ${fg} on ${bg} = ${ratio.toFixed(2)}:1`,
        ).toBeGreaterThanOrEqual(4.5);
      });
    }
  });
}
