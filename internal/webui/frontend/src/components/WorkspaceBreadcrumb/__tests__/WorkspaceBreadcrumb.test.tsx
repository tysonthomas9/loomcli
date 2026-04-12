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
import { getWorkspaceColor } from "@/utils/workspace";

import { WorkspaceBreadcrumb } from "../WorkspaceBreadcrumb";

describe("WorkspaceBreadcrumb", () => {
  describe("with workspace name", () => {
    it("renders the workspace name", () => {
      render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      expect(screen.getByText("my-project")).toBeInTheDocument();
    });

    it("renders the color dot", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      const dot = container.querySelector('[class*="dot"]');
      expect(dot).toBeInTheDocument();
    });

    it("applies the correct background color to the dot", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      const dot = container.querySelector('[class*="dot"]');
      const expectedColor = getWorkspaceColor("my-project");
      expect(dot).toHaveStyle({ backgroundColor: expectedColor });
    });

    it("renders the separator", () => {
      render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      expect(screen.getByText("/")).toBeInTheDocument();
    });

    it("renders the view label", () => {
      render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      expect(screen.getByText("Kanban")).toBeInTheDocument();
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

    it("renders workspace name in element with workspaceName CSS class", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      const nameEl = container.querySelector('[class*="workspaceName"]');
      expect(nameEl).toBeInTheDocument();
      expect(nameEl).toHaveTextContent("my-project");
    });

    it("renders separator in element with separator CSS class", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      const separator = container.querySelector('[class*="separator"]');
      expect(separator).toBeInTheDocument();
      expect(separator).toHaveTextContent("/");
    });

    it("renders view label in element with viewLabel CSS class", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="my-project" activeView="kanban" />,
      );

      const viewLabel = container.querySelector('[class*="viewLabel"]');
      expect(viewLabel).toBeInTheDocument();
      expect(viewLabel).toHaveTextContent("Kanban");
    });
  });

  describe("fallback without workspace name", () => {
    it('renders "Cortex" when workspaceName is null', () => {
      render(<WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />);

      expect(screen.getByText("Cortex")).toBeInTheDocument();
    });

    it("does not render a color dot when workspaceName is null", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />,
      );

      const dot = container.querySelector('[class*="dot"]');
      expect(dot).not.toBeInTheDocument();
    });

    it("does not render a separator when workspaceName is null", () => {
      render(<WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />);

      expect(screen.queryByText("/")).not.toBeInTheDocument();
    });

    it("does not render a view label when workspaceName is null", () => {
      render(<WorkspaceBreadcrumb workspaceName={null} activeView="kanban" />);

      expect(screen.queryByText("Kanban")).not.toBeInTheDocument();
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
      kanban: "Kanban",
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

  describe("dot color", () => {
    it("uses getWorkspaceColor to determine dot background", () => {
      const names = ["project-a", "project-b", "my-workspace"];

      for (const name of names) {
        const { container, unmount } = render(
          <WorkspaceBreadcrumb workspaceName={name} activeView="kanban" />,
        );

        const dot = container.querySelector('[class*="dot"]');
        const expectedColor = getWorkspaceColor(name);
        expect(dot).toHaveStyle({ backgroundColor: expectedColor });

        unmount();
      }
    });

    it("different workspace names can produce different dot colors", () => {
      const { container: c1 } = render(
        <WorkspaceBreadcrumb workspaceName="aaa" activeView="kanban" />,
      );
      const { container: c2 } = render(
        <WorkspaceBreadcrumb workspaceName="zzz" activeView="kanban" />,
      );

      const dot1 = c1.querySelector('[class*="dot"]') as HTMLElement;
      const dot2 = c2.querySelector('[class*="dot"]') as HTMLElement;

      // Both should have valid background colors; they may differ
      expect(dot1.style.backgroundColor).toBeTruthy();
      expect(dot2.style.backgroundColor).toBeTruthy();
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

    it("contains dot, name, separator, and label as children", () => {
      const { container } = render(
        <WorkspaceBreadcrumb workspaceName="test" activeView="graph" />,
      );

      const root = container.firstChild as HTMLElement;
      const children = Array.from(root.children);

      expect(children).toHaveLength(4);
      expect(children[0]?.className).toMatch(/dot/);
      expect(children[1]?.className).toMatch(/workspaceName/);
      expect(children[2]?.className).toMatch(/separator/);
      expect(children[3]?.className).toMatch(/viewLabel/);
    });
  });
});
