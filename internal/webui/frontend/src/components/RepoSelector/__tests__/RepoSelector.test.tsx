/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for RepoSelector component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { RepoSelector } from "../RepoSelector";

describe("RepoSelector", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("rendering", () => {
    it("returns null when availableRepos is empty", () => {
      const onChange = vi.fn();
      const { container } = render(
        <RepoSelector
          availableRepos={[]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      expect(container.innerHTML).toBe("");
    });

    it("returns null when availableRepos has 1 item", () => {
      const onChange = vi.fn();
      const { container } = render(
        <RepoSelector
          availableRepos={["my-repo"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      expect(container.innerHTML).toBe("");
    });

    it('renders trigger button with "Repos" when nothing selected', () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      const trigger = screen.getByTestId("repo-filter-trigger");
      expect(trigger).toHaveTextContent("Repos");
      // Should not show a count
      expect(trigger).not.toHaveTextContent("Repos (");
    });

    it('renders trigger button with "Repos (2)" when 2 repos selected', () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b", "repo-c"]}
          selectedRepos={["repo-a", "repo-b"]}
          onChange={onChange}
        />,
      );

      const trigger = screen.getByTestId("repo-filter-trigger");
      expect(trigger).toHaveTextContent("Repos (2)");
    });
  });

  describe("interactions", () => {
    it("opens dropdown on trigger click", () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      expect(screen.queryByTestId("repo-filter-menu")).not.toBeInTheDocument();

      fireEvent.click(screen.getByTestId("repo-filter-trigger"));

      expect(screen.getByTestId("repo-filter-menu")).toBeInTheDocument();
    });

    it("closes dropdown on second trigger click", () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      const trigger = screen.getByTestId("repo-filter-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-filter-menu")).toBeInTheDocument();

      fireEvent.click(trigger);
      expect(screen.queryByTestId("repo-filter-menu")).not.toBeInTheDocument();
    });

    it("closes dropdown on outside click", () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-filter-trigger"));
      expect(screen.getByTestId("repo-filter-menu")).toBeInTheDocument();

      // Click outside
      fireEvent.mouseDown(document.body);

      expect(screen.queryByTestId("repo-filter-menu")).not.toBeInTheDocument();
    });

    it("each repo renders with a checkbox and RepoBadge", () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={["repo-a"]}
          onChange={onChange}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-filter-trigger"));

      // Verify checkboxes are present via data-testid
      const checkboxA = screen.getByTestId("repo-option-repo-a");
      const checkboxB = screen.getByTestId("repo-option-repo-b");

      expect(checkboxA).toBeInTheDocument();
      expect(checkboxA).toBeChecked();

      expect(checkboxB).toBeInTheDocument();
      expect(checkboxB).not.toBeChecked();

      // Verify RepoBadge is rendered (it renders a span with aria-label)
      expect(screen.getByLabelText("Repository: repo-a")).toBeInTheDocument();
      expect(screen.getByLabelText("Repository: repo-b")).toBeInTheDocument();
    });

    it("checking a repo calls onChange with that repo added", () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-filter-trigger"));
      fireEvent.click(screen.getByTestId("repo-option-repo-a"));

      expect(onChange).toHaveBeenCalledWith(["repo-a"]);
    });

    it("unchecking a repo calls onChange with that repo removed", () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={["repo-a", "repo-b"]}
          onChange={onChange}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-filter-trigger"));
      fireEvent.click(screen.getByTestId("repo-option-repo-a"));

      expect(onChange).toHaveBeenCalledWith(["repo-b"]);
    });
  });

  describe("accessibility", () => {
    it('has role="listbox" and aria-multiselectable="true" on menu', () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-filter-trigger"));

      const menu = screen.getByTestId("repo-filter-menu");
      expect(menu).toHaveAttribute("role", "listbox");
      expect(menu).toHaveAttribute("aria-multiselectable", "true");
    });

    it("trigger has aria-expanded attribute", () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      const trigger = screen.getByTestId("repo-filter-trigger");
      expect(trigger).toHaveAttribute("aria-expanded", "false");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "true");
    });

    it("trigger has aria-haspopup and aria-label attributes", () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      const trigger = screen.getByTestId("repo-filter-trigger");
      expect(trigger).toHaveAttribute("aria-haspopup", "listbox");
      expect(trigger).toHaveAttribute("aria-label", "Filter by repository");
    });
  });

  describe("CSS", () => {
    it("scrollable class present on menu", () => {
      const onChange = vi.fn();
      render(
        <RepoSelector
          availableRepos={["repo-a", "repo-b"]}
          selectedRepos={[]}
          onChange={onChange}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-filter-trigger"));

      const menu = screen.getByTestId("repo-filter-menu");
      // repoMenu class from RepoSelector.module.css provides max-height (scroll)
      expect(menu.className).toMatch(/repoMenu/);
    });
  });
});
