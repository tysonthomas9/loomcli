/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

import { KeyboardCheatsheet } from "../KeyboardCheatsheet";

// ---- Mocks ----

const mockCloseCheatsheet = vi.fn();

vi.mock("@/hooks/useKeyboardShortcuts", () => ({
  useKeyboardShortcuts: vi.fn(() => ({
    isCheatsheetOpen: false,
    toggleCheatsheet: vi.fn(),
    closeCheatsheet: mockCloseCheatsheet,
  })),
  useRegisterEscapeLayer: vi.fn(),
  LAYER_CHEATSHEET: 45,
}));

import { useKeyboardShortcuts } from "@/hooks/useKeyboardShortcuts";

const mockUseKeyboardShortcuts = vi.mocked(useKeyboardShortcuts);

// ---- Tests ----

describe("KeyboardCheatsheet", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseKeyboardShortcuts.mockReturnValue({
      isCheatsheetOpen: false,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: mockCloseCheatsheet,
    } as ReturnType<typeof useKeyboardShortcuts>);
  });

  it("renders nothing when cheatsheet is closed", () => {
    const { container } = render(<KeyboardCheatsheet />);
    // createPortal renders to document.body, not inside the container
    expect(container.firstChild).toBeNull();
    expect(
      document.body.querySelector('[role="dialog"]'),
    ).not.toBeInTheDocument();
  });

  it("renders modal dialog when cheatsheet is open", () => {
    mockUseKeyboardShortcuts.mockReturnValue({
      isCheatsheetOpen: true,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: mockCloseCheatsheet,
    } as ReturnType<typeof useKeyboardShortcuts>);

    render(<KeyboardCheatsheet />);
    expect(
      screen.getByRole("dialog", { name: "Keyboard shortcuts" }),
    ).toBeInTheDocument();
  });

  it("renders title 'Keyboard Shortcuts'", () => {
    mockUseKeyboardShortcuts.mockReturnValue({
      isCheatsheetOpen: true,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: mockCloseCheatsheet,
    } as ReturnType<typeof useKeyboardShortcuts>);

    render(<KeyboardCheatsheet />);
    expect(
      screen.getByRole("heading", { name: "Keyboard Shortcuts" }),
    ).toBeInTheDocument();
  });

  it("renders all shortcut sections", () => {
    mockUseKeyboardShortcuts.mockReturnValue({
      isCheatsheetOpen: true,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: mockCloseCheatsheet,
    } as ReturnType<typeof useKeyboardShortcuts>);

    render(<KeyboardCheatsheet />);
    expect(screen.getByText("Navigation")).toBeInTheDocument();
    expect(screen.getByText("Actions")).toBeInTheDocument();
    expect(screen.getByText("Views")).toBeInTheDocument();
  });

  it("renders navigation shortcuts", () => {
    mockUseKeyboardShortcuts.mockReturnValue({
      isCheatsheetOpen: true,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: mockCloseCheatsheet,
    } as ReturnType<typeof useKeyboardShortcuts>);

    render(<KeyboardCheatsheet />);
    expect(screen.getByText("Kanban board")).toBeInTheDocument();
    expect(screen.getByText("Table view")).toBeInTheDocument();
    expect(screen.getByText("Terminal")).toBeInTheDocument();
    expect(screen.getByText("Observability")).toBeInTheDocument();
    expect(screen.getByText("Files")).toBeInTheDocument();
    expect(screen.getByText("Workspace")).toBeInTheDocument();
    expect(screen.getByText("Settings")).toBeInTheDocument();
  });

  it("renders action shortcuts", () => {
    mockUseKeyboardShortcuts.mockReturnValue({
      isCheatsheetOpen: true,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: mockCloseCheatsheet,
    } as ReturnType<typeof useKeyboardShortcuts>);

    render(<KeyboardCheatsheet />);
    expect(
      screen.getByText("Close panel / modal / dropdown"),
    ).toBeInTheDocument();
    expect(screen.getByText("Focus search")).toBeInTheDocument();
    expect(screen.getByText("Toggle this cheatsheet")).toBeInTheDocument();
  });

  it("renders keyboard key elements with kbd tags", () => {
    mockUseKeyboardShortcuts.mockReturnValue({
      isCheatsheetOpen: true,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: mockCloseCheatsheet,
    } as ReturnType<typeof useKeyboardShortcuts>);

    render(<KeyboardCheatsheet />);
    const kbds = document.querySelectorAll("kbd");
    expect(kbds.length).toBeGreaterThan(0);
    // Check specific keys
    expect(screen.getByText("Esc")).toBeInTheDocument();
    expect(screen.getByText("?")).toBeInTheDocument();
  });

  it("closes when backdrop is mousedown-clicked directly", () => {
    mockUseKeyboardShortcuts.mockReturnValue({
      isCheatsheetOpen: true,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: mockCloseCheatsheet,
    } as ReturnType<typeof useKeyboardShortcuts>);

    render(<KeyboardCheatsheet />);
    // The backdrop is the parent of the dialog. mouseDown on it should close.
    const dialog = screen.getByRole("dialog");
    const backdrop = dialog.parentElement!;
    fireEvent.mouseDown(backdrop, {
      target: backdrop,
      currentTarget: backdrop,
    });
    expect(mockCloseCheatsheet).toHaveBeenCalledTimes(1);
  });

  it("does not close when dialog content is mousedown-clicked", () => {
    mockUseKeyboardShortcuts.mockReturnValue({
      isCheatsheetOpen: true,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: mockCloseCheatsheet,
    } as ReturnType<typeof useKeyboardShortcuts>);

    render(<KeyboardCheatsheet />);
    const dialog = screen.getByRole("dialog");
    // mouseDown on dialog itself (target != currentTarget on backdrop)
    fireEvent.mouseDown(dialog);
    expect(mockCloseCheatsheet).not.toHaveBeenCalled();
  });
});
