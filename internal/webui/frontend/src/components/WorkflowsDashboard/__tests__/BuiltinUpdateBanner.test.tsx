/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { BuiltinVersionsInfo } from "@/api";

import { BuiltinUpdateBanner } from "../BuiltinUpdateBanner";

function builtin(overrides: Partial<BuiltinVersionsInfo> = {}): BuiltinVersionsInfo {
  return {
    packaged_version_id: "epic-runner-v-abc",
    packaged_source_digest: "sha256:1234567890abcdef",
    packaged_artifact_digest: "sha256:art",
    track: "pinned",
    update_available: false,
    previous_active_version_id: "",
    ...overrides,
  };
}

describe("BuiltinUpdateBanner", () => {
  it("renders nothing without built-in info", () => {
    const { container } = render(
      <BuiltinUpdateBanner pending={false} onAdopt={() => undefined} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when no update is available", () => {
    const { container } = render(
      <BuiltinUpdateBanner
        builtin={builtin({ update_available: false })}
        pending={false}
        onAdopt={() => undefined}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows an update banner and fires adopt", () => {
    const onAdopt = vi.fn();
    render(
      <BuiltinUpdateBanner
        builtin={builtin({ update_available: true })}
        pending={false}
        onAdopt={onAdopt}
      />,
    );
    expect(screen.getByTestId("builtin-update-banner")).toHaveAttribute(
      "data-variant",
      "update",
    );
    fireEvent.click(screen.getByTestId("adopt-builtin-update"));
    expect(onAdopt).toHaveBeenCalledTimes(1);
  });

  it("shows a packaging error variant", () => {
    render(
      <BuiltinUpdateBanner
        builtin={builtin({ packaged_error: "reinstall Loom" })}
        pending={false}
        onAdopt={() => undefined}
      />,
    );
    expect(screen.getByTestId("builtin-update-banner")).toHaveAttribute(
      "data-variant",
      "error",
    );
  });
});
