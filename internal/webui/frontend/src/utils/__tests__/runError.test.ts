import { describe, expect, it } from "vitest";

import { humanizeRunError, shortRunErrorLabel } from "../runError";

describe("runError", () => {
  it("gives compact agent-row labels a safe fallback", () => {
    expect(shortRunErrorLabel("BackendUnavailable")).toBe(
      "backend unavailable",
    );
    expect(shortRunErrorLabel("FutureErrorClass")).toBe("run failed");
    expect(shortRunErrorLabel(undefined)).toBeUndefined();
  });

  it("humanizes known and unknown machine identifiers", () => {
    expect(humanizeRunError("local_backend_unavailable")).toBe(
      "Local backend unavailable",
    );
    expect(humanizeRunError("FutureErrorClass")).toBe("Future error class");
  });

  it("preserves detailed error prose", () => {
    expect(
      humanizeRunError("Could not open repo_with_underscore at /tmp/x"),
    ).toBe("Could not open repo_with_underscore at /tmp/x");
  });
});
