/**
 * AgentChat on the real Agent API and the fake harness (playwright.fake-agent
 * .config.ts starts both): a lead on each harness takes a message, the turn
 * streams and ends, a reload rebuilds the same transcript from ListEvents with
 * nothing missing or doubled, the default lead never asks, and all three
 * harnesses render the same transcript.
 */
import { expect, test } from "@playwright/test";

const API = process.env.FAKE_AGENT_API!;

test("a lead chats the same on OpenCode, codex and Claude", async ({
  page,
  request,
}) => {
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
        overrides: { harness },
      },
    });
    expect(res.status(), await res.text()).toBeLessThan(300);
    const { agent_id } = await res.json();

    await page.goto(`/test/agent-chat?ws=w1&agent=${agent_id}`);
    await expect(page.getByTestId("harness-label")).toHaveText(harness);
    const transcript = page.getByTestId("chat-transcript");
    const input = page.getByLabel("Message");
    await input.fill("hi there");
    await input.press("Enter");
    await expect(transcript.getByText("Turn completed")).toHaveCount(1);
    await expect(transcript.getByText("hi there")).toHaveCount(1);
    await expect(page.getByTestId("ask-card")).toHaveCount(0);

    await page.reload();
    await expect(page.getByTestId("harness-label")).toHaveText(harness);
    await expect(transcript.getByText("Turn completed")).toHaveCount(1);
    await expect(transcript.getByText("hi there")).toHaveCount(1);
    await expect(page.getByTestId("ask-card")).toHaveCount(0);
    transcripts.push(await transcript.innerHTML());
  }
  expect(transcripts[1]).toBe(transcripts[0]);
  expect(transcripts[2]).toBe(transcripts[0]);
});
