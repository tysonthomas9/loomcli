/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { WorkflowVersionItem } from "@/api";

import { WorkflowVersionsTable } from "../WorkflowVersionsTable";

function version(
  id: string,
  overrides: Partial<WorkflowVersionItem> = {},
): WorkflowVersionItem {
  return {
    version: { version_id: id, driver_id: "epic-runner", version: 1 },
    active: false,
    approved: false,
    effective_trust: "untrusted",
    bundle_verified: true,
    ...overrides,
  };
}

const noop = () => undefined;

describe("WorkflowVersionsTable", () => {
  it("shows an empty state with no versions", () => {
    render(
      <WorkflowVersionsTable
        versions={[]}
        pending={false}
        onApprove={noop}
        onUnapprove={noop}
        onActivate={noop}
        onRollback={noop}
      />,
    );
    expect(screen.getByTestId("versions-empty")).toBeInTheDocument();
  });

  it("renders an Approve action for an unapproved version and fires it", () => {
    const onApprove = vi.fn();
    render(
      <WorkflowVersionsTable
        versions={[version("v1")]}
        pending={false}
        onApprove={onApprove}
        onUnapprove={noop}
        onActivate={noop}
        onRollback={noop}
      />,
    );
    fireEvent.click(screen.getByTestId("approve-v1"));
    expect(onApprove).toHaveBeenCalledWith("v1");
  });

  it("renders Unapprove for an approved active version, disables activate, and shows no rollback on it", () => {
    render(
      <WorkflowVersionsTable
        versions={[version("v2", { active: true, approved: true })]}
        pending={false}
        onApprove={noop}
        onUnapprove={noop}
        onActivate={noop}
        onRollback={noop}
      />,
    );
    expect(screen.getByTestId("unapprove-v2")).toBeInTheDocument();
    expect(screen.getByTestId("activate-v2")).toBeDisabled();
    // "Roll back to" is only meaningful for an OLDER version; the active version
    // (and here the only version) never offers it.
    expect(screen.queryByTestId("rollback-v2")).not.toBeInTheDocument();
  });

  it("offers rollback only on versions older than the active one, and fires it", () => {
    const onRollback = vi.fn();
    render(
      <WorkflowVersionsTable
        versions={[
          version("v2", {
            version: { version_id: "v2", driver_id: "epic-runner", version: 2 },
            active: true,
            approved: true,
          }),
          version("v1", {
            version: { version_id: "v1", driver_id: "epic-runner", version: 1 },
          }),
        ]}
        pending={false}
        onApprove={noop}
        onUnapprove={noop}
        onActivate={noop}
        onRollback={onRollback}
      />,
    );
    // Older version v1 (below active v2) offers rollback; active v2 does not.
    expect(screen.queryByTestId("rollback-v2")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("rollback-v1"));
    expect(onRollback).toHaveBeenCalledWith("v1");
    // v1 can also just be activated.
    expect(screen.getByTestId("activate-v1")).toBeEnabled();
  });

  it("disables every action while an action is pending", () => {
    render(
      <WorkflowVersionsTable
        versions={[version("v1")]}
        pending={true}
        onApprove={noop}
        onUnapprove={noop}
        onActivate={noop}
        onRollback={noop}
      />,
    );
    expect(screen.getByTestId("approve-v1")).toBeDisabled();
    expect(screen.getByTestId("activate-v1")).toBeDisabled();
  });
});
