import { test, expect } from "@playwright/test";

// Guards the E2E_REUSE_SERVER=1 path: confirm whatever answers on the dev-server
// port is actually the loom frontend, not some other service that happens to
// hold the port. See PUPPET-217.
test("dev server on the configured port is the loom frontend", async ({
  request,
  baseURL,
}) => {
  let body: string;
  let status: number;
  try {
    const res = await request.get("/", { maxRedirects: 0 });
    status = res.status();
    body = await res.text();
  } catch (err) {
    throw new Error(
      `Could not reach the Vite dev server at ${baseURL}: ${String(err)}\n` +
        `Free the port, or run the suite elsewhere with ` +
        `E2E_DEV_PORT=3200 npm run test:e2e`,
    );
  }
  const looksLikeLoom =
    body.includes('id="root"') && body.includes("<title>Loom");
  expect(
    looksLikeLoom,
    `${baseURL} answered HTTP ${status} but this is not the loom frontend.\n` +
      `Received: ${body.slice(0, 200)}\n` +
      `Something else is holding that port (PUPPET-217). Either free it, or run ` +
      `the suite on another port with E2E_DEV_PORT=3200 npm run test:e2e.`,
  ).toBe(true);
});
