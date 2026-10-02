/**
 * AgentChat on the real Agent API and the fake harness (playwright.fake-agent
 * .config.ts starts both): a lead on each harness takes a plain, a hostile
 * HTML and a long message, each turn runs and ends, the messages render as
 * literal wrapped text, a reload rebuilds the same transcript from ListEvents
 * with nothing missing or doubled, the default lead never asks, and all three
 * harnesses render the same transcript. The fake streams its reply only as a
 * live delta and saves no completed reply item, so after a reload the saved
 * user messages are what this proves; reply-item recovery is not claimed.
 */
import { expect, test } from "@playwright/test";

const API = process.env.FAKE_AGENT_API!;
const XSS = `<img src=x onerror="window.pwned=1"><script>window.pwned=1</script>`;
const LONG = "y".repeat(5000);
const MESSAGES = ["hi there", XSS, LONG];

test("a lead chats the same on OpenCode, codex and Claude", async ({
  page,
  request,
}) => {
  let dialogs = 0;
  page.on("dialog", (d) => {
    dialogs++;
    void d.dismiss();
  });
  await page.route("**/api/config", (r) =>
    r.fulfill({ json: { mode: "open" } }),
  );
  await page.route("**/api/workspaces/w1/events/token", (r) =>
    r.fulfill({ status: 404 }),
  );
  const transcripts: string[] = [];
  for (const harness of ["opencode", "codex", "claude"]) {
    const res = await request.post(`${API}/api/workspaces/w1/v1/agents`, {
      headers: { "Idempotency-Key": `create-${harness}-${Date.now()}` },
      data: {
        preset: "lead",
        name: `lead-${harness}`,
        repo: "demo",
        base_ref: "main",
        overrides: { harness },
      },
    });
    expect(res.status(), await res.text()).toBeLessThan(300);
    const { agent_id } = await res.json();

    await page.goto(`/test/agent-chat?ws=w1&agent=${agent_id}`);
    await expect(page.getByTestId("harness-label")).toHaveText(harness);
    const transcript = page.getByTestId("chat-transcript");
    const input = page.getByLabel("Message");
    const turnsDone = async () => {
      const r = await request.get(
        `${API}/api/workspaces/w1/v1/agents/${agent_id}/events`,
      );
      const { events } = await r.json();
      return events.filter(
        (e: { kind: string }) => e.kind === "agent.turn_completed",
      ).length;
    };
    // Each message runs one turn that ends on the server; a completed turn
    // adds no transcript line.
    for (const [n, text] of MESSAGES.entries()) {
      await input.fill(text);
      await input.press("Enter");
      await expect.poll(turnsDone).toBe(n + 1);
    }
    await expect(page.getByText("idle", { exact: true })).toBeVisible();

    const check = async () => {
      for (const text of MESSAGES)
        await expect(transcript.getByText(text, { exact: true })).toHaveCount(
          1,
        );
      await expect(transcript.locator("img, script")).toHaveCount(0);
      await expect(page.getByTestId("ask-card")).toHaveCount(0);
      expect(
        await page.evaluate(() => (window as { pwned?: number }).pwned),
      ).toBeUndefined();
      expect(
        await transcript.evaluate((el) => el.scrollWidth - el.clientWidth),
      ).toBeLessThanOrEqual(0);
    };
    await check();
    await page.reload();
    await expect(page.getByTestId("harness-label")).toHaveText(harness);
    await check();
    transcripts.push(await transcript.innerHTML());
  }
  expect(transcripts[1]).toBe(transcripts[0]);
  expect(transcripts[2]).toBe(transcripts[0]);
  expect(dialogs).toBe(0);
});
