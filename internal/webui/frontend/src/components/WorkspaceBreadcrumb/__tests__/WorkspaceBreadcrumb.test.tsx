/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceBreadcrumb component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import type { ViewMode } from "@/components/ViewSwitcher";

import { WorkspaceBreadcrumb } from "../WorkspaceBreadcrumb";

describe("WorkspaceBreadcrumb", () => {
  describe("with workspace name", () => {
    it("renders the view label", () => {
      render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      expect(screen.getByText("Aether Project")).toBeInTheDocument();
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

    it("renders view label in element with viewLabel CSS class", () => {
      const { container } = render(
        <WorkspaceBreadcrumb
          workspaceName="my-project"
          activeView="terminal"
        />,
      );

      const viewLabel = container.querySelector('[class*="viewLabel"]');
      expect(viewLabel).toBeInTheDocument();
      expect(viewLabel).toHaveTextContent("Monitor");
    });
  });

  describe("fallback without workspace name", () => {
    it('renders "Aether" when workspaceName is null', () => {
      render(<WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />);

      expect(screen.getByText("Aether")).toBeInTheDocument();
    });

    it("does not render a view label when workspaceName is null", () => {
      render(<WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />);

      expect(screen.queryByText("Aether Project")).not.toBeInTheDocument();
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

  describe("view labels", () => {
    const viewLabelMap: Record<ViewMode, string> = {
      kanban: "Aether Project",
      table: "List",
      graph: "Graph",
      monitor: "Monitor",
      observability: "Observability",
      terminal: "Monitor",
      workspace: "Workspace",
      settings: "Settings",
      files: "Files",
      "issue-detail": "Issue",
    };

    for (const [viewMode, expectedLabel] of Object.entries(viewLabelMap)) {
      it(`renders "${expectedLabel}" for view mode "${viewMode}"`, () => {
        render(
          <WorkspaceBreadcrumb
            workspaceName="test"
            activeView={viewMode as ViewMode}
          />,
        );

        expect(screen.getByText(expectedLabel)).toBeInTheDocument();
      });
    }
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

    it("contains only the view label as child", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="test" activeView="graph" />,
      );

      const root = container.firstChild as HTMLElement;
      const children = Array.from(root.children);

      expect(children).toHaveLength(1);
      expect(children[0]?.className).toMatch(/viewLabel/);
    });
  });
});
