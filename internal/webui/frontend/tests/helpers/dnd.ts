import type { Locator, Page } from '@playwright/test';

export interface DragOptions {
  /** Pixels past dnd-kit activation threshold. Must exceed 5. Default: 10. */
  activationDistance?: number;
  /** Interpolated mouse.move steps from activation to target. Default: 10. */
  steps?: number;
}

/**
 * Drag `source` onto `target` using real CDP pointer events that
 * @dnd-kit's PointerSensor (activationConstraint: { distance: 5 })
 * recognises. Replaces synthetic `page.dispatchEvent('body', 'pointermove', …)`
 * sequences, which fail to reach document-level sensor listeners.
 *
 * Callers await any side effects (PATCH waitForResponse, UI assertions)
 * separately — this helper only owns the pointer choreography.
 *
 * Background: loomcli-7rth3.3.
 */
export async function dragWithPointer(
  page: Page,
  source: Locator,
  target: Locator,
  options: DragOptions = {},
): Promise<void> {
  const { activationDistance = 10, steps = 10 } = options;
  if (activationDistance < 6) {
    throw new Error(
      `dragWithPointer: activationDistance must exceed @dnd-kit PointerSensor distance (5), got ${activationDistance}`,
    );
  }

  await target.scrollIntoViewIfNeeded();
  const sourceBox = await source.boundingBox();
  const targetBox = await target.boundingBox();
  if (!sourceBox || !targetBox) {
    throw new Error(
      'dragWithPointer: could not obtain bounding box for source or target',
    );
  }

  const startX = sourceBox.x + sourceBox.width / 2;
  const startY = sourceBox.y + sourceBox.height / 2;
  const endX = targetBox.x + targetBox.width / 2;
  const endY = targetBox.y + targetBox.height / 2;

  await source.hover();
  await page.mouse.down();
  await page.mouse.move(startX + activationDistance, startY);
  await page.mouse.move(endX, endY, { steps });
  await page.mouse.up();
}
