import { describe, expect, it } from "vitest";

import {
  deliveryAncestryCue,
  reviewPrDeepLink,
  stackGroupDeepLink,
} from "../deliveryAncestryCue";

describe("deliveryAncestryCue", () => {
  it("returns null when there is no previous member", () => {
    expect(
      deliveryAncestryCue({
        currentRepo: "octocat/hello",
        prevRepo: null,
        currentBaseRef: "feat-a",
        prevHeadRef: "feat-a",
      }),
    ).toBeNull();
  });

  it("labels same-repo base==prev head as based on prev branch", () => {
    expect(
      deliveryAncestryCue({
        currentRepo: "octocat/hello",
        prevRepo: "octocat/hello",
        currentBaseRef: "feat-a",
        prevHeadRef: "feat-a",
      }),
    ).toBe("based on prev branch");
  });

  it("labels same-repo without matching refs as delivery after prev", () => {
    expect(
      deliveryAncestryCue({
        currentRepo: "Octocat/Hello",
        prevRepo: "octocat/hello",
        currentBaseRef: "main",
        prevHeadRef: "feat-a",
      }),
    ).toBe("delivery after prev");
  });

  it("labels different repos as cross-repo delivery", () => {
    expect(
      deliveryAncestryCue({
        currentRepo: "octocat/world",
        prevRepo: "octocat/hello",
        currentBaseRef: "feat-a",
        prevHeadRef: "feat-a",
      }),
    ).toBe("cross-repo delivery");
  });
});

describe("deep links", () => {
  it("builds review-pr and group+pr targets with group id and pr key", () => {
    expect(reviewPrDeepLink("WS", "github:octocat/hello#7")).toBe(
      "/ws/WS/prs?review-pr=octocat%2Fhello%237",
    );
    expect(
      stackGroupDeepLink(
        "WS",
        "dg_01HABCDEFGHJKLMNPQRSTUVWXY",
        "github:octocat/hello#7",
      ),
    ).toBe(
      "/ws/WS/prs?group=dg_01HABCDEFGHJKLMNPQRSTUVWXY&pr=github%3Aoctocat%2Fhello%237",
    );
  });
});
