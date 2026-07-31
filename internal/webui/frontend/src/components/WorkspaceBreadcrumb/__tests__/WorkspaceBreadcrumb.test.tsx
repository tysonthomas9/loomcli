/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceBreadcrumb component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { WorkspaceBreadcrumb } from "../WorkspaceBreadcrumb";

describe("WorkspaceBreadcrumb", () => {
  describe("with workspace name", () => {
    it("renders the Loom brand label", () => {
      render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      expect(screen.getByText("Loom")).toBeInTheDocument();
    });

    it("does not render the workspace name (that lives in the sidebar selector)", () => {
      render(
        <WorkspaceBreadcrumb
          workspaceName="my-project"
          activeView="terminal"
        />,
      );

      expect(screen.queryByText("my-project")).not.toBeInTheDocument();
    });

    it("does not render a color dot (workspace identity lives in the sidebar selector)", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      const dot = container.querySelector('[class*="dot"]');
      expect(dot).not.toBeInTheDocument();
    });

    it("applies the breadcrumb CSS class to root span", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      const root = container.firstChild as HTMLElement;
      expect(root.className).toMatch(/breadcrumb/);
    });

    it("applies custom className alongside breadcrumb class", () => {
      const { container } = render(
        <WorkspaceBreadcrumb
          workspaceName="my-project"
          activeView="kanban"
          className="custom-class"
        />,
      );

      const root = container.firstChild as HTMLElement;
      expect(root.className).toMatch(/breadcrumb/);
      expect(root).toHaveClass("custom-class");
    });

    it("renders brand label in element with viewLabel CSS class", () => {
      const { container } = render(
        <WorkspaceBreadcrumb
          workspaceName="my-project"
          activeView="terminal"
        />,
      );

      const viewLabel = container.querySelector('[class*="viewLabel"]');
      expect(viewLabel).toBeInTheDocument();
      expect(viewLabel).toHaveTextContent("Loom");
    });
  });

  describe("fallback without workspace name", () => {
    it('renders "Loom" when workspaceName is null', () => {
      render(<WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />);

      expect(screen.getByText("Loom")).toBeInTheDocument();
    });

    it("renders the shared brand mark when workspaceName is null", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName={null} activeView="agents" />,
      );

      expect(container.textContent).toContain("◇");
      expect(screen.getByText("Loom")).toBeInTheDocument();
    });

    it("does not render a route label when workspaceName is null", () => {
      render(<WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />);

      expect(screen.queryByText("Loom Project")).not.toBeInTheDocument();
    });

    it("applies breadcrumb CSS class when workspaceName is null for layout consistency", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />,
      );

      const root = container.firstChild as HTMLElement;
      expect(root.className).toMatch(/breadcrumb/);
    });

    it("applies custom className when workspaceName is null", () => {
      const { container } = render(
        <WorkspaceBreadcrumb
          workspaceName={null}
          activeView="kanban"
          className="custom-class"
        />,
      );

      const root = container.firstChild as HTMLElement;
      expect(root).toHaveClass("custom-class");
    });
  });

  describe("route consistency", () => {
    it("renders Loom instead of the active route label", () => {
      render(<WorkspaceBreadcrumb workspaceName="test" activeView="agents" />);

      expect(screen.getByText("Loom")).toBeInTheDocument();
      expect(screen.queryByText("Agents")).not.toBeInTheDocument();
    });
  });

  describe("structure", () => {
    it("renders root as a span element", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="test" activeView="kanban" />,
      );

      const root = container.firstChild as HTMLElement;
      expect(root.tagName).toBe("SPAN");
    });

    it("renders fallback as a span element", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />,
      );

      const root = container.firstChild as HTMLElement;
      expect(root.tagName).toBe("SPAN");
    });

    it("contains the brand mark and view label as children", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="test" activeView="graph" />,
      );

      const root = container.firstChild as HTMLElement;
      const children = Array.from(root.children);

      expect(children).toHaveLength(2);
      expect(children[0]?.className).toMatch(/brandMark/);
      expect(children[1]?.className).toMatch(/viewLabel/);
    });
  });
});
