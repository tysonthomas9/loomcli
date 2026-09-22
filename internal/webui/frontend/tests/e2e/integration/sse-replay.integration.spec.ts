import { test, expect } from "@playwright/test";
import {
  createTestIssueInWorkspace,
  closeTestIssueInWorkspace,
  resolveWorkspaceId,
  generateTestId,
} from "./helpers";

type Delivery = { id: string; issue: string; type: string };
type ReplayProbe = {
  deliveries: Delivery[];
  barriers: Delivery[][];
  urls: string[];
  cut: () => void;
};
declare global {
  interface Window {
    __sseReplay: ReplayProbe;
  }
}

// Real product writes and native EventSource delivery. The only injected fault
// closes the transport; the application/client/page remain alive throughout.
// Set SSE_REPLAY_DISABLE_DELIVERY=1 for the deliberately failing negative control.
test("persistent browser drains paginated replay before synchronized", async ({
  page,
}, testInfo) => {
  test.skip(
    !process.env.RUN_INTEGRATION_TESTS,
    "requires a running local stack",
  );
  test.setTimeout(180_000);
  const ws = await resolveWorkspaceId();
  const prefix = `SSE replay ${generateTestId()}`;
  const created: string[] = [];
  let blocked = false;
  await page.route(
    /\/api\/workspaces\/[^/]+\/events(?:\?.*)?$/,
    async (route) => {
      if (blocked) await route.abort("connectionfailed");
      else await route.continue();
    },
  );
  await page.addInitScript(() => {
    const Native = window.EventSource;
    const sources: EventSource[] = [];
    const probe: ReplayProbe = {
      deliveries: [],
      barriers: [],
      urls: [],
      cut: () => {
        for (const source of sources) {
          source.close();
          source.dispatchEvent(new Event("error"));
        }
      },
    };
    window.__sseReplay = probe;
    window.EventSource = class extends Native {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        if (!/\/events(?:\?|$)/.test(String(url))) return;
        sources.push(this);
        probe.urls.push(String(url));
        this.addEventListener("mutation", (event) => {
          const data = JSON.parse((event as MessageEvent).data);
          probe.deliveries.push({
            id: (event as MessageEvent).lastEventId,
            issue: data.issue_id,
            type: data.type,
          });
        });
        this.addEventListener("connected", () =>
          probe.barriers.push([...probe.deliveries]),
        );
      }
    };
  });
  try {
    await page.goto(`/ws/${ws}/kanban`);
    await expect
      .poll(() => page.evaluate(() => window.__sseReplay.barriers.length))
      .toBeGreaterThan(0);
    const seed = await createTestIssueInWorkspace(ws, `${prefix} seed`);
    created.push(seed);
    await expect
      .poll(() =>
        page.evaluate(
          (id) =>
            window.__sseReplay.deliveries.some(
              (e) => e.issue === id && e.type === "create",
            ),
          seed,
        ),
      )
      .toBe(true);
    blocked = true;
    const before = await page.evaluate(() => {
      const probe = window.__sseReplay;
      const snapshot = {
        deliveries: probe.deliveries.length,
        barriers: probe.barriers.length,
        connections: probe.urls.length,
        cursor: probe.deliveries.at(-1)!.id,
      };
      probe.cut();
      return snapshot;
    });
    expect(await page.evaluate(() => window.__sseReplay.barriers.length)).toBe(
      before.barriers,
    );
    // Sequential committed API writes give an independent expected order.
    for (let n = 0; n < 201; n++)
      created.push(
        await createTestIssueInWorkspace(ws, `${prefix} backlog ${n}`),
      );
    const backlog = created.slice(1);
    const navigations: string[] = [];
    page.on("framenavigated", (frame) => {
      if (frame === page.mainFrame()) navigations.push(frame.url());
    });
    blocked = process.env.SSE_REPLAY_DISABLE_DELIVERY === "1";
    // Additional product writes race with the reconnect/replay window.
    for (let n = 0; n < 10; n++)
      created.push(
        await createTestIssueInWorkspace(ws, `${prefix} concurrent ${n}`),
      );
    const expected = created.slice(1);
    await expect
      .poll(
        async () =>
          page.evaluate(
            (ids) =>
              window.__sseReplay.deliveries
                .filter((e) => e.type === "create" && ids.includes(e.issue))
                .map((e) => e.issue),
            expected,
          ),
        { timeout: 45_000 },
      )
      .toEqual(expected);
    await expect
      .poll(() => page.evaluate(() => window.__sseReplay.barriers.length))
      .toBeGreaterThan(before.barriers);
    const proof = await page.evaluate(() => window.__sseReplay);
    const firstBarrier = proof.barriers[before.barriers];
    expect(
      firstBarrier,
      "reconnect must emit a completion barrier",
    ).toBeDefined();
    expect(
      firstBarrier
        .filter((e) => e.type === "create" && backlog.includes(e.issue))
        .map((e) => e.issue),
    ).toEqual(backlog);
    const delivered = proof.deliveries
      .slice(before.deliveries)
      .filter((e) => expected.includes(e.issue) && e.type === "create");
    expect(delivered.every((e) => e.id.length > 0)).toBe(true);
    expect(new Set(delivered.map((e) => e.id)).size).toBe(expected.length);
    expect(
      proof.urls
        .slice(1)
        .some((url) => new URL(url, page.url()).searchParams.has("since")),
    ).toBe(true);
    expect(
      new URL(proof.urls[before.connections], page.url()).searchParams.get(
        "since",
      ),
    ).toBe(before.cursor);
    expect(navigations).toEqual([]);
    await testInfo.attach("sse-replay-proof", {
      body: JSON.stringify(
        {
          expected,
          delivered,
          before,
          barriers: proof.barriers,
          urls: proof.urls,
        },
        null,
        2,
      ),
      contentType: "application/json",
    });
  } finally {
    await testInfo.attach("sse-delivery-on-exit", {
      body: JSON.stringify(
        await page.evaluate(() => ({
          deliveries: window.__sseReplay?.deliveries,
          barriers: window.__sseReplay?.barriers,
          urls: window.__sseReplay?.urls,
        })),
      ),
      contentType: "application/json",
    });
    for (const id of created) await closeTestIssueInWorkspace(ws, id);
  }
});
