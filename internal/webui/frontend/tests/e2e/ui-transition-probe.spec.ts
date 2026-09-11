import { expect, test } from "@playwright/test";

import { observeTransition } from "../helpers/ui-transition-probe";

const scope = { workspace: "probe-fixture", selectedIssue: "A", generation: 1 };

async function loadedFixture(page: Parameters<typeof observeTransition>[0]) {
  await page.setContent(`
    <main id="stable"><section data-testid="panel">
      <button data-testid="protected">Save</button><div class="content">Loaded</div>
    </section></main>
  `);
}

test("probe accepts unchanged loaded DOM", async ({ page }) => {
  await loadedFixture(page);
  const probe = await observeTransition(page, {
    root: '[data-testid="panel"]',
    protectedNodes: ['[data-testid="protected"]'],
    forbiddenWithinRoot: [".spinner"],
    scope,
  });
  await probe.assertSatisfied();
  await probe.dispose();
});

test("probe catches synchronous add/remove of forbidden loading UI", async ({
  page,
}) => {
  await loadedFixture(page);
  const probe = await observeTransition(page, {
    root: '[data-testid="panel"]',
    forbiddenWithinRoot: [".spinner"],
    scope,
  });
  await page.evaluate(() => {
    const spinner = document.createElement("div");
    spinner.className = "spinner";
    document.querySelector('[data-testid="panel"]')!.append(spinner);
    spinner.remove();
  });
  await expect(probe.assertSatisfied()).rejects.toThrow(
    "forbidden-node-inserted",
  );
});

test("probe catches a forbidden descendant after its wrapper is detached", async ({
  page,
}) => {
  await loadedFixture(page);
  const probe = await observeTransition(page, {
    root: '[data-testid="panel"]',
    forbiddenWithinRoot: [".spinner"],
    scope,
  });
  await page.evaluate(() => {
    const panel = document.querySelector('[data-testid="panel"]')!;
    const wrapper = document.createElement("div");
    panel.append(wrapper);
    const spinner = document.createElement("div");
    spinner.className = "spinner";
    wrapper.append(spinner);
    wrapper.remove();
  });
  await expect(probe.assertSatisfied()).rejects.toThrow(
    "forbidden-node-inserted",
  );
});

test("probe catches an off-DOM spinner removed before its wrapper", async ({
  page,
}) => {
  await loadedFixture(page);
  const probe = await observeTransition(page, {
    root: '[data-testid="panel"]',
    forbiddenWithinRoot: [".spinner"],
    scope,
  });
  await page.evaluate(() => {
    const wrapper = document.createElement("div");
    const spinner = document.createElement("div");
    spinner.className = "spinner";
    wrapper.append(spinner);
    document.querySelector('[data-testid="panel"]')!.append(wrapper);
    spinner.remove();
    wrapper.remove();
  });
  await expect(probe.assertSatisfied()).rejects.toThrow(
    "forbidden-node-inserted",
  );
});

test("probe catches ancestor replacement and protected-node replacement", async ({
  page,
}) => {
  await loadedFixture(page);
  const probe = await observeTransition(page, {
    root: '[data-testid="panel"]',
    protectedNodes: ['[data-testid="protected"]'],
    scope,
  });
  await page.evaluate(() => {
    document.querySelector("#stable")!.innerHTML = `
      <section data-testid="panel"><button data-testid="protected">Save</button></section>`;
  });
  await expect(probe.assertSatisfied()).rejects.toThrow(
    /root-removed|root-identity-changed/,
  );
});

test("probe catches removal even when the same root is reinserted", async ({
  page,
}) => {
  await loadedFixture(page);
  const probe = await observeTransition(page, {
    root: '[data-testid="panel"]',
    scope,
  });
  await page.evaluate(() => {
    const stable = document.querySelector("#stable")!;
    const panel = document.querySelector('[data-testid="panel"]')!;
    panel.remove();
    stable.append(panel);
  });
  await expect(probe.assertSatisfied()).rejects.toThrow("root-removed");
});

test("probe allows semantic relocation when node identity is not protected", async ({
  page,
}) => {
  await loadedFixture(page);
  await page.evaluate(() => {
    const panel = document.querySelector('[data-testid="panel"]')!;
    panel.insertAdjacentHTML(
      "beforeend",
      '<div id="open"></div><div id="closed"></div>',
    );
  });
  const probe = await observeTransition(page, {
    root: '[data-testid="panel"]',
    scope,
  });
  await page.evaluate(() => {
    const card = document.createElement("article");
    card.dataset.issue = "A";
    document.querySelector("#open")!.append(card);
    document.querySelector("#closed")!.append(card);
  });
  await probe.assertSatisfied();
  await probe.dispose();
});

test("probe stops same-scope identity checks after explicit retirement", async ({
  page,
}) => {
  await loadedFixture(page);
  const probe = await observeTransition(page, {
    root: '[data-testid="panel"]',
    protectedNodes: ['[data-testid="protected"]'],
    scope,
  });
  await probe.retireScope();
  await page.evaluate(() => {
    document.querySelector("#stable")!.innerHTML =
      '<div data-testid="loading-next">Loading B</div>';
  });
  await probe.assertSatisfied();
  await probe.dispose();
});

test("probe rejects a root that has not reached loaded readiness", async ({
  page,
}) => {
  await loadedFixture(page);
  await page.evaluate(() => {
    document
      .querySelector('[data-testid="panel"]')!
      .insertAdjacentHTML("beforeend", '<div class="spinner"></div>');
  });
  await expect(
    observeTransition(page, {
      root: '[data-testid="panel"]',
      forbiddenWithinRoot: [".spinner"],
      scope,
    }),
  ).rejects.toThrow("not ready");
});

test("probe fails on record overflow", async ({ page }) => {
  await loadedFixture(page);
  const overflow = await observeTransition(page, {
    root: '[data-testid="panel"]',
    maxRecords: 1,
    scope,
  });
  await page.evaluate(() => {
    const panel = document.querySelector('[data-testid="panel"]')!;
    panel.append(document.createElement("i"));
    panel.append(document.createElement("b"));
  });
  await expect(overflow.assertSatisfied()).rejects.toThrow("record-overflow");
});

test("probe fails on premature disposal", async ({ page }) => {
  await loadedFixture(page);
  const premature = await observeTransition(page, {
    root: '[data-testid="panel"]',
    scope,
  });
  await expect(premature.dispose()).rejects.toThrow(
    "disposed before assertion",
  );
});

test("probe drains violations introduced after an earlier assertion", async ({
  page,
}) => {
  await loadedFixture(page);
  const probe = await observeTransition(page, {
    root: '[data-testid="panel"]',
    forbiddenWithinRoot: [".spinner"],
    scope,
  });
  await probe.assertSatisfied();
  await page.evaluate(() => {
    document
      .querySelector('[data-testid="panel"]')!
      .insertAdjacentHTML("beforeend", '<div class="spinner"></div>');
  });
  await expect(probe.dispose()).rejects.toThrow("forbidden-node-inserted");
});
