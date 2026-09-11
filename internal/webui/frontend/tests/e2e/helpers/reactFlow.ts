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
 * React Flow cannot compute edge paths until its ResizeObserver has committed
 * node dimensions and handle bounds to the internal store. Reading a positive
 * DOM rectangle does not prove that commit happened. If positive edges remain
 * absent after measurement, temporarily change each observed node wrapper by
 * one pixel so the browser runs layout and notifies that observer. Window and
 * viewport resize events are insufficient when fixed-size nodes do not change.
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
  if (expectedEdgeCount === 0) {
    await expect(edges).toHaveCount(0, { timeout: EDGE_TIMEOUT_MS });
    return;
  }

  try {
    await expect(edges).toHaveCount(expectedEdgeCount, {
      timeout: EDGE_LAYOUT_NUDGE_DELAY_MS,
    });
    return;
  } catch {
    // Only recover the observed initialization failure. A non-zero wrong count
    // is a product/test-data failure and must reach the final assertion below.
    if ((await edges.count()) === 0) {
      await page.evaluate(async (nodeSelector) => {
        const nextFrame = () =>
          new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        const renderedNodes = Array.from(
          document.querySelectorAll<HTMLElement>(nodeSelector),
        );
        const previousWidths = renderedNodes.map((node) => node.style.width);

        for (const node of renderedNodes) {
          node.style.width = `${node.getBoundingClientRect().width + 1}px`;
        }
        await nextFrame();
        await nextFrame();

        renderedNodes.forEach((node, index) => {
          node.style.width = previousWidths[index] ?? "";
        });
        await nextFrame();
        await nextFrame();
      }, REACT_FLOW_NODE_SELECTOR);
    }
  }

  await expect(edges).toHaveCount(expectedEdgeCount, {
    timeout: EDGE_TIMEOUT_MS - EDGE_LAYOUT_NUDGE_DELAY_MS,
  });
}
