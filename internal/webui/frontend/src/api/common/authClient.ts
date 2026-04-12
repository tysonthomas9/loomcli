/**
 * Better Auth client singleton for external OAuth authentication.
 *
 * Initialized lazily via initExternalAuth() when the server reports
 * mode='external'. Includes jwtClient() plugin so authClient.token()
 * can exchange session cookies for RS256 JWTs.
 */

import { createAuthClient } from "better-auth/react";
import { jwtClient } from "better-auth/client/plugins";

// ============= Types =============

type AuthClient = ReturnType<
  typeof createAuthClient<{
    plugins: [ReturnType<typeof jwtClient>];
  }>
>;

// ============= Singleton =============

let client: AuthClient | null = null;

/**
 * Create and cache the Better Auth client for external OAuth.
 * Idempotent — calling twice returns the same singleton.
 */
export function initExternalAuth(authUrl: string): AuthClient {
  if (client) return client;
  client = createAuthClient({
    baseURL: authUrl,
    fetchOptions: {
      credentials: "include",
    },
    plugins: [jwtClient()],
  });
  return client;
}

/**
 * Get the initialized auth client. Throws if initExternalAuth()
 * has not been called — this is a programming error.
 */
export function getAuthClient(): AuthClient {
  if (!client) {
    throw new Error(
      "Auth client not initialized. Call initExternalAuth() first.",
    );
  }
  return client;
}
