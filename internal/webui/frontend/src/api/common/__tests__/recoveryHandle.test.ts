import { describe, expect, it } from "vitest";
import { decodeRecoveryHandle } from "../recoveryHandle";
const now = Date.parse("2026-09-05T12:00:00Z");
function offer() {
  return {
    handle: "A".repeat(43),
    workspace: "WS",
    source_repos: ["a", "b"],
    expires_at: "2026-09-05T12:01:00Z",
    manifest: "fleet.issue-workspace.v1",
  };
}
describe("decodeRecoveryHandle", () => {
  it("matches wire scope and defensively freezes", () => {
    const input = offer();
    input.source_repos = ["b", "a", "b"];
    const result = decodeRecoveryHandle(input, "WS", [" a,b ", "a"], now);
    expect(result).toEqual(input);
    expect(Object.isFrozen(result)).toBe(true);
    expect(Object.isFrozen(result?.source_repos)).toBe(true);
    input.source_repos[0] = "changed";
    expect(result?.source_repos).toEqual(["b", "a", "b"]);
  });
  it.each([
    ["missing", { handle: undefined }],
    ["noncanonical", { handle: "A".repeat(42) + "B" }],
    ["foreign", { workspace: "OTHER" }],
    ["manifest", { manifest: "other" }],
    ["expired", { expires_at: "2026-09-05T12:00:00Z" }],
    ["invalid date", { expires_at: "2026-02-30T12:00:00Z" }],
    ["offset", { expires_at: "2026-09-05T12:01:00+00:00" }],
    ["scope", { source_repos: ["a"] }],
    ["untrimmed", { source_repos: [" a", "b"] }],
    ["empty repo", { source_repos: ["a", "b", ""] }],
    ["extra", { extra: true }],
    ["null repos", { source_repos: null }],
  ])("rejects %s", (_name, patch) => {
    expect(
      decodeRecoveryHandle({ ...offer(), ...patch }, "WS", ["a", "b"], now),
    ).toBeUndefined();
  });
  it("allows empty scope and clock skew without an upper expiry cap", () => {
    expect(
      decodeRecoveryHandle(
        {
          ...offer(),
          source_repos: [],
          expires_at: "2026-09-05T12:05:00.123456789Z",
        },
        "WS",
        undefined,
        now,
      ),
    ).toBeDefined();
  });
  it.each([undefined, null, [], "offer", {}])(
    "rejects malformed shapes",
    (input) => {
      expect(decodeRecoveryHandle(input, "WS", [], now)).toBeUndefined();
    },
  );
});
