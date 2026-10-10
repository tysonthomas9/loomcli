import { describe, expect, it } from "vitest";

import {
  AGENT_COLOR_COUNT,
  agentColor,
  agentColorIndex,
  agentInitials,
} from "../agentColor";

describe("agentColor", () => {
  it("gives an agent the same colour every time, from its id", () => {
    const id = "agt_01J9ZK4R7T2V";
    expect(agentColorIndex(id)).toBe(agentColorIndex(id));
    expect(agentColor(id)).toBe(`var(--agent-color-${agentColorIndex(id)})`);
  });

  it("stays in the palette and varies across ids", () => {
    const ids = Array.from({ length: 40 }, (_, i) => `agt_child_${i}`);
    const slots = ids.map(agentColorIndex);
    for (const s of slots) {
      expect(s).toBeGreaterThanOrEqual(0);
      expect(s).toBeLessThan(AGENT_COLOR_COUNT);
    }
    // 40 ids spread over most of the 8 slots, not one or two.
    expect(new Set(slots).size).toBeGreaterThanOrEqual(6);
    expect(agentColorIndex("k1")).not.toBe(agentColorIndex("k2"));
  });

  it("takes initials from a name's first and last parts", () => {
    expect(agentInitials("ui-test-agent-1")).toBe("U1");
    expect(agentInitials("ui-test-agent-2")).toBe("U2");
    expect(agentInitials("Local-Coder")).toBe("LC");
    expect(agentInitials("kid")).toBe("KI");
    expect(agentInitials("  ")).toBe("?");
  });
});
