/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

import { CompactRailHost } from "../CompactRailHost";

describe("CompactRailHost", () => {
  it("renders a hover tooltip label in a portal", () => {
    render(<CompactRailHost label="hello-world">H</CompactRailHost>);
    const host = screen.getByLabelText("hello-world");
    expect(host).toHaveTextContent("H");
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    fireEvent.mouseEnter(host);
    expect(screen.getByRole("tooltip")).toHaveTextContent("hello-world");
  });
});
