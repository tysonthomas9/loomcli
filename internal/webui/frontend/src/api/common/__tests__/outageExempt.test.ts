import { describe, expect, it } from "vitest";

import { isOutageExemptPath, wsUrl } from "../client";

/**
 * isOutageExemptPath decides whether a 503 is an expected answer or a workspace
 * outage. Over-matching would silence a real outage, so the negative cases
 * matter as much as the positive ones.
 */
describe("isOutageExemptPath", () => {
  it("exempts the claim-hold route", () => {
    expect(isOutageExemptPath("/api/workspaces/PUPPET/claims/hold")).toBe(true);
  });

  it("exempts the claim-hold route with a query string", () => {
    // releaseClaimHold appends ?actor=…&force=true, and fetchApi matches on the
    // raw path — an endsWith() matcher would miss this one.
    expect(
      isOutageExemptPath(
        "/api/workspaces/PUPPET/claims/hold?actor=a&force=true",
      ),
    ).toBe(true);
  });

  it("exempts terminal routes", () => {
    expect(isOutageExemptPath("/api/workspaces/PUPPET/terminal/token")).toBe(
      true,
    );
    expect(isOutageExemptPath("/api/workspaces/PUPPET/terminal/sessions")).toBe(
      true,
    );
  });

  it("does not exempt ordinary workspace routes", () => {
    expect(isOutageExemptPath("/api/workspaces/PUPPET/issues")).toBe(false);
    expect(isOutageExemptPath("/api/health")).toBe(false);
    expect(isOutageExemptPath("/api/workspaces/PUPPET/agents")).toBe(false);
  });

  it("does not exempt a path that merely contains the word hold", () => {
    expect(isOutageExemptPath("/api/workspaces/PUPPET/issues/hold")).toBe(
      false,
    );
    expect(isOutageExemptPath("/api/workspaces/PUPPET/claims")).toBe(false);
  });

  it("does not exempt a workspace whose id looks like the route", () => {
    // wsUrl percent-encodes the id, so a workspace named "claims/hold" cannot
    // forge the route suffix.
    expect(isOutageExemptPath(wsUrl("claims/hold", "/issues"))).toBe(false);
  });
});
