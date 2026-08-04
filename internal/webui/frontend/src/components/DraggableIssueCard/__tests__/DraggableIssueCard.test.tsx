/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for DraggableIssueCard component.
 */

import { DndContext, useDraggable } from "@dnd-kit/core";
import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, type Mock } from "vitest";

import type { Issue } from "@/types";

import { DraggableIssueCard } from "../DraggableIssueCard";

// Mock useDraggable hook
vi.mock("@dnd-kit/core", async () => {
  const actual = await vi.importActual("@dnd-kit/core");
  return {
    ...actual,
    useDraggable: vi.fn(),
  };
});

const mockedUseDraggable = useDraggable as Mock;

/**
 * Create a mock issue for testing.
 */
function createMockIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "test-issue-123",
    title: "Test Issue Title",
    priority: 2,
    status: "open",
    issue_type: "task",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

/**
 * Create a mock useDraggable return value.
 */
function createMockDraggableReturn(
  overrides: Partial<ReturnType<typeof useDraggable>> = {},
) {
  return {
    attributes: {
      role: "button",
      tabIndex: 0,
      "aria-pressed": undefined,
      "aria-roledescription": "draggable",
      "aria-describedby": "dnd-describedby-1",
    },
    listeners: {
      onKeyDown: vi.fn(),
      onPointerDown: vi.fn(),
    },
    setNodeRef: vi.fn(),
    transform: null,
    isDragging: false,
    ...overrides,
  };
}

/**
 * Helper to render DraggableIssueCard within a DndContext.
 */
function renderWithDndContext(ui: React.ReactNode) {
  return render(<DndContext>{ui}</DndContext>);
}

function getCardElement(container: HTMLElement): HTMLElement {
  return container.querySelector("article") as HTMLElement;
}

describe("DraggableIssueCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedUseDraggable.mockReturnValue(createMockDraggableReturn());
  });

  describe("rendering", () => {
    it("renders IssueCard as the root article", () => {
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      // IssueCard should render with the issue title
      expect(screen.getByText("Test Issue Title")).toBeInTheDocument();
      // IssueCard renders as an article (dnd-kit adds role="button" for dragging)
      expect(container.querySelector("article")).toBeInTheDocument();
    });

    it("passes issue prop through to IssueCard", () => {
      const mockIssue = createMockIssue({
        title: "My Custom Title",
        priority: 1,
      });

      render(<DraggableIssueCard issue={mockIssue} />);

      expect(screen.getByText("My Custom Title")).toBeInTheDocument();
      // Priority surfaces only as a data attribute (no visible badge).
      expect(screen.queryByText("P1")).not.toBeInTheDocument();
      expect(
        screen.getByText("My Custom Title").closest("article"),
      ).toHaveAttribute("data-priority", "1");
    });

    it("passes onClick prop through to IssueCard", () => {
      const mockIssue = createMockIssue();
      const handleClick = vi.fn();

      render(<DraggableIssueCard issue={mockIssue} onClick={handleClick} />);

      // IssueCard should have button role when onClick is provided
      const card = screen.getByLabelText(/Issue: Test Issue Title/i);
      expect(card).toBeInTheDocument();
    });

    it("applies data-dragging attribute based on state", () => {
      const mockIssue = createMockIssue();
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ isDragging: true }),
      );

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveAttribute("data-dragging", "true");
    });

    it("does not apply data-dragging when not dragging", () => {
      const mockIssue = createMockIssue();
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ isDragging: false }),
      );

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).not.toHaveAttribute("data-dragging");
    });
  });

  describe("drag behavior (useDraggable hook)", () => {
    it("useDraggable is called with correct id (issue.id)", () => {
      const mockIssue = createMockIssue({ id: "unique-issue-456" });

      render(<DraggableIssueCard issue={mockIssue} />);

      expect(mockedUseDraggable).toHaveBeenCalledWith(
        expect.objectContaining({
          id: "unique-issue-456",
        }),
      );
    });

    it("useDraggable is called with data containing issue", () => {
      const mockIssue = createMockIssue();

      render(<DraggableIssueCard issue={mockIssue} />);

      expect(mockedUseDraggable).toHaveBeenCalledWith(
        expect.objectContaining({
          data: expect.objectContaining({
            issue: mockIssue,
            type: "issue",
          }),
        }),
      );
    });

    it("listeners are applied to the full card", () => {
      const mockListeners = {
        onKeyDown: vi.fn(),
        onPointerDown: vi.fn(),
      };
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ listeners: mockListeners }),
      );
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveProperty("onkeydown");
      expect(card).toHaveProperty("onpointerdown");
    });

    it("attributes are applied to the full card", () => {
      const mockAttributes = {
        role: "button",
        tabIndex: 0,
        "aria-roledescription": "draggable",
        "aria-describedby": "dnd-describedby-test",
      };
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ attributes: mockAttributes }),
      );
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveAttribute("role", "button");
      expect(card).toHaveAttribute("tabIndex", "0");
      expect(card).toHaveAttribute("aria-roledescription", "draggable");
      expect(card).toHaveAttribute("aria-describedby", "dnd-describedby-test");
    });

    it("setNodeRef is applied to the card element", () => {
      const setNodeRef = vi.fn();
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ setNodeRef }),
      );
      const mockIssue = createMockIssue();

      render(<DraggableIssueCard issue={mockIssue} />);

      // setNodeRef should be called with the card DOM element
      expect(setNodeRef).toHaveBeenCalled();
    });
  });

  describe("visual state", () => {
    it("has opacity: 0.5 when isDragging is true", () => {
      const mockIssue = createMockIssue();
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ isDragging: true }),
      );

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveStyle({ opacity: "0.5" });
    });

    it("has opacity: 1 when not dragging", () => {
      const mockIssue = createMockIssue();
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ isDragging: false }),
      );

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveStyle({ opacity: "1" });
    });

    it("applies transform when transform is present", () => {
      const mockIssue = createMockIssue();
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({
          transform: { x: 100, y: 50, scaleX: 1, scaleY: 1 },
        }),
      );

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveStyle({
        transform: "translate3d(100px, 50px, 0)",
      });
    });

    it("no transform style when transform is null", () => {
      const mockIssue = createMockIssue();
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ transform: null }),
      );

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      // transform should be undefined (not applied)
      expect(card.style.transform).toBe("");
    });

    it("applies data-draggable attribute", () => {
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveAttribute("data-draggable", "true");
    });
  });

  describe("overlay mode", () => {
    it("isOverlay=true renders without drag listeners", () => {
      const mockListeners = {
        onKeyDown: vi.fn(),
        onPointerDown: vi.fn(),
      };
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ listeners: mockListeners }),
      );
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard issue={mockIssue} isOverlay={true} />,
      );

      const wrapper = container.firstChild as HTMLElement;
      // In overlay mode, listeners should NOT be applied
      expect(wrapper).not.toHaveAttribute("onkeydown");
      expect(wrapper).not.toHaveAttribute("onpointerdown");
    });

    it("isOverlay mode renders with overlay CSS class", () => {
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard issue={mockIssue} isOverlay={true} />,
      );

      const wrapper = container.firstChild as HTMLElement;
      expect(wrapper.className).toContain("overlay");
    });

    it("isOverlay mode does not have draggable CSS class", () => {
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard issue={mockIssue} isOverlay={true} />,
      );

      const wrapper = container.firstChild as HTMLElement;
      // Overlay uses 'overlay' class, not 'draggable'
      expect(wrapper.className).not.toContain("draggable");
    });

    it("isOverlay mode still renders IssueCard", () => {
      const mockIssue = createMockIssue({ title: "Overlay Card Title" });

      render(<DraggableIssueCard issue={mockIssue} isOverlay={true} />);

      expect(screen.getByText("Overlay Card Title")).toBeInTheDocument();
    });

    it("isOverlay mode does not have setNodeRef applied", () => {
      const setNodeRef = vi.fn();
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ setNodeRef }),
      );
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard issue={mockIssue} isOverlay={true} />,
      );

      // In overlay mode, the wrapper should not have ref set
      const wrapper = container.firstChild as HTMLElement;
      // setNodeRef is still called by the hook, but wrapper won't have it as ref prop
      expect(wrapper).not.toHaveAttribute("ref");
    });

    it("isOverlay defaults to false", () => {
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveAttribute("data-draggable", "true");
    });
  });

  describe("accessibility", () => {
    it("ARIA attributes from useDraggable are present on the card", () => {
      const mockAttributes = {
        role: "button",
        tabIndex: 0,
        "aria-roledescription": "draggable",
        "aria-describedby": "dnd-describedby-test",
      };
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ attributes: mockAttributes }),
      );
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveAttribute("aria-roledescription", "draggable");
      expect(card).toHaveAttribute("aria-describedby", "dnd-describedby-test");
    });

    it("card is focusable via tabIndex", () => {
      const mockAttributes = {
        tabIndex: 0,
      };
      mockedUseDraggable.mockReturnValue(
        createMockDraggableReturn({ attributes: mockAttributes }),
      );
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).toHaveAttribute("tabIndex", "0");
    });
  });

  describe("DndContext integration", () => {
    it("component works within DndContext", () => {
      // Use mock that returns valid draggable state
      mockedUseDraggable.mockReturnValue(createMockDraggableReturn());
      const mockIssue = createMockIssue();

      // This should not throw
      expect(() => {
        renderWithDndContext(<DraggableIssueCard issue={mockIssue} />);
      }).not.toThrow();

      expect(screen.getByText("Test Issue Title")).toBeInTheDocument();
    });

    it("component renders within DndContext with draggable card", () => {
      mockedUseDraggable.mockReturnValue(createMockDraggableReturn());
      const mockIssue = createMockIssue();

      const { container } = renderWithDndContext(
        <DraggableIssueCard issue={mockIssue} />,
      );

      const card = getCardElement(container);
      expect(card).toHaveAttribute("data-draggable", "true");
    });

    it("multiple DraggableIssueCards work in same DndContext", () => {
      mockedUseDraggable.mockReturnValue(createMockDraggableReturn());
      const issue1 = createMockIssue({ id: "issue-1", title: "First Issue" });
      const issue2 = createMockIssue({ id: "issue-2", title: "Second Issue" });
      const issue3 = createMockIssue({ id: "issue-3", title: "Third Issue" });

      renderWithDndContext(
        <>
          <DraggableIssueCard issue={issue1} />
          <DraggableIssueCard issue={issue2} />
          <DraggableIssueCard issue={issue3} />
        </>,
      );

      expect(screen.getByText("First Issue")).toBeInTheDocument();
      expect(screen.getByText("Second Issue")).toBeInTheDocument();
      expect(screen.getByText("Third Issue")).toBeInTheDocument();
    });

    it("useDraggable is called for each card with unique ids", () => {
      mockedUseDraggable.mockReturnValue(createMockDraggableReturn());
      const issue1 = createMockIssue({ id: "issue-a", title: "Issue A" });
      const issue2 = createMockIssue({ id: "issue-b", title: "Issue B" });

      renderWithDndContext(
        <>
          <DraggableIssueCard issue={issue1} />
          <DraggableIssueCard issue={issue2} />
        </>,
      );

      // useDraggable should be called twice with different ids
      expect(mockedUseDraggable).toHaveBeenCalledWith(
        expect.objectContaining({ id: "issue-a" }),
      );
      expect(mockedUseDraggable).toHaveBeenCalledWith(
        expect.objectContaining({ id: "issue-b" }),
      );
    });
  });

  describe("props", () => {
    it("className prop is passed through to IssueCard", () => {
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard issue={mockIssue} className="custom-class" />,
      );

      const article = container.querySelector("article");
      expect(article).toHaveClass("custom-class");
    });

    it("works with minimal issue props", () => {
      const minimalIssue: Issue = {
        id: "minimal-id",
        title: "Minimal Issue",
        priority: 3,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      };

      render(<DraggableIssueCard issue={minimalIssue} />);

      expect(screen.getByText("Minimal Issue")).toBeInTheDocument();
    });

    it("handles issue with all optional fields", () => {
      const fullIssue = createMockIssue({
        description: "Full description",
        labels: ["bug", "urgent"],
        assignee: "developer@example.com",
      });

      render(<DraggableIssueCard issue={fullIssue} />);

      expect(screen.getByText("Test Issue Title")).toBeInTheDocument();
    });

    it("renders working agent footer badge via IssueCard", () => {
      const issue = createMockIssue({
        owner: "tyson",
        assignee: "lead-a",
      });

      render(<DraggableIssueCard issue={issue} columnId="in_progress" />);

      expect(screen.getByTestId("issue-card-agent")).toHaveAttribute(
        "title",
        "Working: lead-a",
      );
    });
  });

  describe("full-card drag", () => {
    it("does not render a separate drag handle", () => {
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      expect(
        container.querySelector('span[class*="dragHandle"]'),
      ).not.toBeInTheDocument();
    });

    it("marks the card as draggable outside overlay mode", () => {
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      expect(getCardElement(container)).toHaveAttribute(
        "data-draggable",
        "true",
      );
    });

    it("does not mark overlay cards as draggable", () => {
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard issue={mockIssue} isOverlay={true} />,
      );

      const article = container.querySelector("article");
      expect(article).not.toHaveAttribute("data-draggable");
    });
  });

  describe("edge cases", () => {
    it("handles empty title", () => {
      const mockIssue = createMockIssue({ title: "" });

      render(<DraggableIssueCard issue={mockIssue} />);

      // IssueCard should show 'Untitled' for empty title
      expect(screen.getByText("Untitled")).toBeInTheDocument();
    });

    it("handles very long issue id", () => {
      const longId = "very-long-issue-id-that-should-be-truncated-123456789";
      const mockIssue = createMockIssue({ id: longId });

      render(<DraggableIssueCard issue={mockIssue} />);

      expect(mockedUseDraggable).toHaveBeenCalledWith(
        expect.objectContaining({
          id: longId,
        }),
      );
    });

    it("handles priority 0 (critical) via data attribute", () => {
      const mockIssue = createMockIssue({ priority: 0 });

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      expect(screen.queryByText("P0")).not.toBeInTheDocument();
      expect(container.querySelector("article")).toHaveAttribute(
        "data-priority",
        "0",
      );
    });

    it("handles priority 4 (backlog) via data attribute", () => {
      const mockIssue = createMockIssue({ priority: 4 });

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      expect(screen.queryByText("P4")).not.toBeInTheDocument();
      expect(container.querySelector("article")).toHaveAttribute(
        "data-priority",
        "4",
      );
    });

    it("handles undefined onClick gracefully", () => {
      const mockIssue = createMockIssue();

      // Should not throw when onClick is undefined
      expect(() => {
        render(<DraggableIssueCard issue={mockIssue} onClick={undefined} />);
      }).not.toThrow();
    });
  });

  describe("pending optimistic state", () => {
    it("applies data-optimistic='pending' when isPending is true", () => {
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard issue={mockIssue} isPending={true} />,
      );

      const card = getCardElement(container);
      expect(card).toHaveAttribute("data-optimistic", "pending");
    });

    it("does not apply data-optimistic when isPending is false", () => {
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard issue={mockIssue} isPending={false} />,
      );

      const card = getCardElement(container);
      expect(card).not.toHaveAttribute("data-optimistic");
    });

    it("does not apply data-optimistic when isPending is undefined", () => {
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const card = getCardElement(container);
      expect(card).not.toHaveAttribute("data-optimistic");
    });

    it("data-optimistic is not set in overlay mode even with isPending", () => {
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard
          issue={mockIssue}
          isOverlay={true}
          isPending={true}
        />,
      );

      const wrapper = container.firstChild as HTMLElement;
      // Overlay mode has different rendering path, no data-optimistic
      expect(wrapper).not.toHaveAttribute("data-optimistic");
    });
  });

  describe("blocked props", () => {
    it("passes blockedByCount to IssueCard", () => {
      const mockIssue = createMockIssue();

      render(<DraggableIssueCard issue={mockIssue} blockedByCount={3} />);

      // BlockedBadge should be rendered with count
      expect(screen.getByLabelText("Blocked by 3 issues")).toBeInTheDocument();
    });

    it("passes blockedBy array to IssueCard", () => {
      const mockIssue = createMockIssue();
      const blockers = ["blocker-1", "blocker-2"];

      render(
        <DraggableIssueCard
          issue={mockIssue}
          blockedByCount={2}
          blockedBy={blockers}
        />,
      );

      // Hover to show tooltip
      const badge = screen.getByLabelText("Blocked by 2 issues");
      fireEvent.mouseEnter(badge);

      expect(screen.getByText("blocker-1")).toBeInTheDocument();
      expect(screen.getByText("blocker-2")).toBeInTheDocument();
    });

    it("does not render BlockedBadge when blockedByCount is undefined", () => {
      const mockIssue = createMockIssue();

      render(<DraggableIssueCard issue={mockIssue} />);

      expect(screen.queryByLabelText(/Blocked by/)).not.toBeInTheDocument();
    });

    it("does not render BlockedBadge when blockedByCount is 0", () => {
      const mockIssue = createMockIssue();

      render(<DraggableIssueCard issue={mockIssue} blockedByCount={0} />);

      expect(screen.queryByLabelText(/Blocked by/)).not.toBeInTheDocument();
    });

    it("passes blocked props in overlay mode", () => {
      const mockIssue = createMockIssue();

      render(
        <DraggableIssueCard
          issue={mockIssue}
          blockedByCount={5}
          blockedBy={["b1", "b2", "b3", "b4", "b5"]}
          isOverlay={true}
        />,
      );

      // BlockedBadge should still render in overlay mode
      expect(screen.getByLabelText("Blocked by 5 issues")).toBeInTheDocument();
    });

    it("IssueCard has data-blocked attribute when blocked", () => {
      const mockIssue = createMockIssue();

      const { container } = render(
        <DraggableIssueCard issue={mockIssue} blockedByCount={1} />,
      );

      const article = container.querySelector("article");
      expect(article).toHaveAttribute("data-blocked", "true");
    });

    it("IssueCard does not have data-blocked when not blocked", () => {
      const mockIssue = createMockIssue();

      const { container } = render(<DraggableIssueCard issue={mockIssue} />);

      const article = container.querySelector("article");
      expect(article).not.toHaveAttribute("data-blocked");
    });
  });
});
