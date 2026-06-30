import { describe, expect, it } from "vitest";

import { getCompactAvatarInitials } from "../compactAvatarInitials";

describe("getCompactAvatarInitials", () => {
  it("uses the first character from the first two segments", () => {
    expect(getCompactAvatarInitials("Hello-World")).toBe("HW");
    expect(getCompactAvatarInitials("lead-a")).toBe("LA");
    expect(getCompactAvatarInitials("test-lead")).toBe("TL");
    expect(getCompactAvatarInitials("local-coder")).toBe("LC");
  });

  it("uses the first two characters for single-segment names", () => {
    expect(getCompactAvatarInitials("loomcli")).toBe("LO");
    expect(getCompactAvatarInitials("planner")).toBe("PL");
  });

  it("handles empty and short names", () => {
    expect(getCompactAvatarInitials("")).toBe("?");
    expect(getCompactAvatarInitials("   ")).toBe("?");
    expect(getCompactAvatarInitials("a")).toBe("A");
  });
});
