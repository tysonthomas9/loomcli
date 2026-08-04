/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for authClient.ts — Better Auth client singleton.
 *
 * Mocks better-auth/react and better-auth/client/plugins modules.
 * Uses vi.resetModules() + dynamic import to reset the singleton.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

const mockJwtClientResult = { id: "jwt-client-plugin" };
const mockClient = { token: vi.fn(), useSession: vi.fn() };

let initExternalAuth: typeof import("../authClient").initExternalAuth;
let getAuthClient: typeof import("../authClient").getAuthClient;

describe("authClient", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    vi.doMock("better-auth/react", () => ({
      createAuthClient: vi.fn(() => mockClient),
    }));
    vi.doMock("better-auth/client/plugins", () => ({
      jwtClient: vi.fn(() => mockJwtClientResult),
    }));

    const mod = await import("../authClient");
    initExternalAuth = mod.initExternalAuth;
    getAuthClient = mod.getAuthClient;
  });

  it("creates client with correct baseURL, credentials, and jwtClient plugin", async () => {
    const { createAuthClient } = await import("better-auth/react");
    const { jwtClient } = await import("better-auth/client/plugins");

    initExternalAuth("https://auth.example.com");

    expect(createAuthClient).toHaveBeenCalledTimes(1);
    expect(createAuthClient).toHaveBeenCalledWith({
      baseURL: "https://auth.example.com",
      fetchOptions: {
        credentials: "include",
      },
      plugins: [mockJwtClientResult],
    });
    expect(jwtClient).toHaveBeenCalledTimes(1);
  });

  it("returns same singleton on repeated calls", async () => {
    const { createAuthClient } = await import("better-auth/react");

    const first = initExternalAuth("https://auth.example.com");
    const second = initExternalAuth("https://auth.example.com");

    expect(first).toBe(second);
    expect(createAuthClient).toHaveBeenCalledTimes(1);
  });

  it("getAuthClient returns initialized client", () => {
    const client = initExternalAuth("https://auth.example.com");
    const retrieved = getAuthClient();

    expect(retrieved).toBe(client);
  });

  it("getAuthClient throws when not initialized", () => {
    expect(() => getAuthClient()).toThrow(
      "Auth client not initialized. Call initExternalAuth() first.",
    );
  });

  it("client has token method from jwtClient plugin", () => {
    initExternalAuth("https://auth.example.com");
    const client = getAuthClient();

    expect(typeof client.token).toBe("function");
  });
});
