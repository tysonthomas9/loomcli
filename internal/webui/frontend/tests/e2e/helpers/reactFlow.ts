import { expect, type Page } from "@playwright/test";

const REACT_FLOW_NODE_SELECTOR = ".react-flow__node";
const EDGE_TIMEOUT_MS = 15_000;
const EDGE_LAYOUT_NUDGE_DELAY_MS = 2_000;

interface ExpectReactFlowEdgeCountOptions {
  edgeSelector: ".react-flow__edge" | ".react-flow__edge-path";
  expectedEdgeCount: number;
  expectedNodeCount?: number;
}

/**
 * Wait for React Flow to mount and measure its nodes before asserting edges.
 *
 * React Flow cannot compute edge paths until node dimensions are available. If
 * positive edges remain absent after measurement, dispatch one resize event to
 * recover from a zero-size-at-mount layout without hiding a genuinely wrong
 * final edge count.
 */
export async function expectReactFlowEdgeCount(
  page: Page,
  {
    edgeSelector,
    expectedEdgeCount,
    expectedNodeCount,
  }: ExpectReactFlowEdgeCountOptions,
) {
  const nodes = page.locator(REACT_FLOW_NODE_SELECTOR);

  if (expectedNodeCount === undefined) {
    await expect
      .poll(() => nodes.count(), { timeout: EDGE_TIMEOUT_MS })
      .toBeGreaterThan(0);
  } else {
    await expect(nodes).toHaveCount(expectedNodeCount, {
      timeout: EDGE_TIMEOUT_MS,
    });
  }

  await page.waitForFunction(
    (nodeSelector) => {
      const renderedNodes = Array.from(
        document.querySelectorAll<HTMLElement>(nodeSelector),
      );

      return (
        renderedNodes.length > 0 &&
        renderedNodes.every((node) => node.getBoundingClientRect().width > 0)
      );
    },
    REACT_FLOW_NODE_SELECTOR,
    { timeout: EDGE_TIMEOUT_MS },
  );

  const edges = page.locator(edgeSelector);
  const edgeCountAssertion = expect(edges).toHaveCount(expectedEdgeCount, {
    timeout: EDGE_TIMEOUT_MS,
  });

  if (expectedEdgeCount === 0) {
    await edgeCountAssertion;
    return;
  }

  let nudgeTimer: ReturnType<typeof setTimeout> | undefined;
  const nudgeFailure = new Promise<never>((_resolve, reject) => {
    nudgeTimer = setTimeout(() => {
      void (async () => {
        if ((await edges.count()) === 0) {
          await page.evaluate(() => window.dispatchEvent(new Event("resize")));
        }
      })().catch(reject);
    }, EDGE_LAYOUT_NUDGE_DELAY_MS);
  });

  try {
    await Promise.race([edgeCountAssertion, nudgeFailure]);
  } finally {
    if (nudgeTimer !== undefined) clearTimeout(nudgeTimer);
  }
}
