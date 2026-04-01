/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for UserMenu component.
 *
 * The component self-gates: it returns null when mode !== 'external',
 * !isAuthenticated, or !user. When rendered it shows an avatar trigger
 * that opens a dropdown with user info and a Sign Out button.
 */

import {
  render,
  screen,
  fireEvent,
  act,
  waitFor,
} from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { UserMenu } from "../UserMenu";

// ---------------------------------------------------------------------------
// Mock @/contexts/AuthContext
// ---------------------------------------------------------------------------
vi.mock("@/contexts/AuthContext", () => ({
  useAuth: vi.fn(),
}));

// Mock @/utils/colorUtils to return a deterministic color
vi.mock("@/utils/colorUtils", () => ({
  getAvatarColor: vi.fn(() => "#9DC08B"),
  shouldUseWhiteText: vi.fn(() => false),
}));

import { useAuth } from "@/contexts/AuthContext";

const mockUseAuth = vi.mocked(useAuth);

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type AuthMode = "open" | "oidc";

interface MockUser {
  id: string;
  name: string;
  email: string;
  image?: string;
}

function buildAuthState(overrides?: {
  mode?: AuthMode;
  user?: MockUser | null;
  isAuthenticated?: boolean;
  signOut?: () => Promise<void>;
}) {
  return {
    mode: overrides?.mode ?? "oidc",
    user:
      overrides?.user !== undefined
        ? overrides.user
        : {
            id: "user-1",
            name: "Alice Example",
            email: "alice@example.com",
            image: undefined,
          },
    isAuthenticated: overrides?.isAuthenticated ?? true,
    isLoading: false,
    authServiceDown: false,
    signIn: vi.fn(),
    signOut: overrides?.signOut ?? vi.fn().mockResolvedValue(undefined),
  };
}

const DEFAULT_USER: MockUser = {
  id: "user-1",
  name: "Alice Example",
  email: "alice@example.com",
};

beforeEach(() => {
  vi.clearAllMocks();
  // Default: authenticated external user, no image
  mockUseAuth.mockReturnValue(buildAuthState());
});

// ---------------------------------------------------------------------------
// Self-gating (returns null)
// ---------------------------------------------------------------------------

describe("UserMenu — self-gating (returns null)", () => {
  it("returns null when mode is 'none'", () => {
    mockUseAuth.mockReturnValue(buildAuthState({ mode: "open" }));
    const { container } = render(<UserMenu />);
    expect(container.firstChild).toBeNull();
  });

  it("returns null when isAuthenticated is false", () => {
    mockUseAuth.mockReturnValue(buildAuthState({ isAuthenticated: false }));
    const { container } = render(<UserMenu />);
    expect(container.firstChild).toBeNull();
  });

  it("returns null when user is null", () => {
    mockUseAuth.mockReturnValue(buildAuthState({ user: null }));
    const { container } = render(<UserMenu />);
    expect(container.firstChild).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Rendering the trigger
// ---------------------------------------------------------------------------

describe("UserMenu — trigger rendering", () => {
  it("renders the trigger button when authenticated", () => {
    render(<UserMenu />);
    expect(screen.getByTestId("user-menu-trigger")).toBeInTheDocument();
  });

  it("shows user name in trigger", () => {
    render(<UserMenu />);
    expect(screen.getByTestId("user-menu-trigger")).toHaveTextContent(
      "Alice Example",
    );
  });

  it("falls back to email as display name when name is empty", () => {
    mockUseAuth.mockReturnValue(
      buildAuthState({
        user: { id: "u2", name: "", email: "bob@example.com" },
      }),
    );
    render(<UserMenu />);
    expect(screen.getByTestId("user-menu-trigger")).toHaveTextContent(
      "bob@example.com",
    );
  });

  it("renders initial-letter avatar when user has no image URL", () => {
    render(<UserMenu />);
    // No <img> tag for avatar
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    // Initial letter "A" from "Alice Example"
    const trigger = screen.getByTestId("user-menu-trigger");
    expect(trigger).toHaveTextContent("A");
  });

  it("renders image avatar when user has an image URL", () => {
    mockUseAuth.mockReturnValue(
      buildAuthState({
        user: { ...DEFAULT_USER, image: "https://example.com/avatar.png" },
      }),
    );
    const { container } = render(<UserMenu />);
    // The img has alt="" so its ARIA role is presentation; query by tag name
    const img = container.querySelector("img");
    expect(img).toBeInTheDocument();
    expect(img).toHaveAttribute("src", "https://example.com/avatar.png");
  });

  it("falls back to initial letter when image fails to load", () => {
    mockUseAuth.mockReturnValue(
      buildAuthState({
        user: { ...DEFAULT_USER, image: "https://example.com/broken.png" },
      }),
    );
    const { container } = render(<UserMenu />);

    const img = container.querySelector("img");
    expect(img).toBeInTheDocument();
    // Simulate image load error
    act(() => {
      fireEvent.error(img!);
    });

    // Image should be replaced by the initial-letter span
    expect(container.querySelector("img")).not.toBeInTheDocument();
    expect(screen.getByTestId("user-menu-trigger")).toHaveTextContent("A");
  });

  it("trigger has aria-haspopup='menu'", () => {
    render(<UserMenu />);
    expect(screen.getByTestId("user-menu-trigger")).toHaveAttribute(
      "aria-haspopup",
      "menu",
    );
  });

  it("trigger has aria-expanded='false' when dropdown is closed", () => {
    render(<UserMenu />);
    expect(screen.getByTestId("user-menu-trigger")).toHaveAttribute(
      "aria-expanded",
      "false",
    );
  });

  it("trigger has aria-expanded='true' when dropdown is open", () => {
    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));
    expect(screen.getByTestId("user-menu-trigger")).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });
});

// ---------------------------------------------------------------------------
// Dropdown open / close
// ---------------------------------------------------------------------------

describe("UserMenu — dropdown open/close", () => {
  it("does not show dropdown initially", () => {
    render(<UserMenu />);
    expect(screen.queryByTestId("user-menu-dropdown")).not.toBeInTheDocument();
  });

  it("opens dropdown on trigger click", () => {
    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));
    expect(screen.getByTestId("user-menu-dropdown")).toBeInTheDocument();
  });

  it("closes dropdown on second trigger click", () => {
    render(<UserMenu />);
    const trigger = screen.getByTestId("user-menu-trigger");
    fireEvent.click(trigger);
    expect(screen.getByTestId("user-menu-dropdown")).toBeInTheDocument();
    fireEvent.click(trigger);
    expect(screen.queryByTestId("user-menu-dropdown")).not.toBeInTheDocument();
  });

  it("closes dropdown on Escape key press", () => {
    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));
    expect(screen.getByTestId("user-menu-dropdown")).toBeInTheDocument();

    act(() => {
      fireEvent.keyDown(document, { key: "Escape" });
    });

    expect(screen.queryByTestId("user-menu-dropdown")).not.toBeInTheDocument();
  });

  it("returns focus to trigger after Escape key closes dropdown", () => {
    render(<UserMenu />);
    const trigger = screen.getByTestId("user-menu-trigger");
    fireEvent.click(trigger);

    act(() => {
      fireEvent.keyDown(document, { key: "Escape" });
    });

    expect(document.activeElement).toBe(trigger);
  });

  it("closes dropdown on click outside the wrapper", () => {
    render(
      <div>
        <UserMenu />
        <button data-testid="outside">Outside</button>
      </div>,
    );
    fireEvent.click(screen.getByTestId("user-menu-trigger"));
    expect(screen.getByTestId("user-menu-dropdown")).toBeInTheDocument();

    act(() => {
      fireEvent.mouseDown(screen.getByTestId("outside"));
    });

    expect(screen.queryByTestId("user-menu-dropdown")).not.toBeInTheDocument();
  });

  it("does not close dropdown on click inside the wrapper", () => {
    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));

    // Click somewhere inside the dropdown (the dropdown itself)
    act(() => {
      fireEvent.mouseDown(screen.getByTestId("user-menu-dropdown"));
    });

    expect(screen.getByTestId("user-menu-dropdown")).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Dropdown content
// ---------------------------------------------------------------------------

describe("UserMenu — dropdown content", () => {
  beforeEach(() => {
    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));
  });

  it("dropdown has role='menu'", () => {
    expect(screen.getByTestId("user-menu-dropdown")).toHaveAttribute(
      "role",
      "menu",
    );
  });

  it("shows user display name in dropdown header", () => {
    // There should be at least one element containing the user name in the dropdown
    const dropdown = screen.getByTestId("user-menu-dropdown");
    expect(dropdown).toHaveTextContent("Alice Example");
  });

  it("shows user email in dropdown header", () => {
    const dropdown = screen.getByTestId("user-menu-dropdown");
    expect(dropdown).toHaveTextContent("alice@example.com");
  });

  it("Sign Out button is present in dropdown", () => {
    expect(screen.getByTestId("user-menu-sign-out")).toBeInTheDocument();
  });

  it("Sign Out button has role='menuitem'", () => {
    expect(screen.getByTestId("user-menu-sign-out")).toHaveAttribute(
      "role",
      "menuitem",
    );
  });

  it("Sign Out button shows 'Sign Out' text when not signing out", () => {
    expect(screen.getByTestId("user-menu-sign-out")).toHaveTextContent(
      "Sign Out",
    );
  });
});

// ---------------------------------------------------------------------------
// Sign Out behaviour
// ---------------------------------------------------------------------------

describe("UserMenu — sign out", () => {
  it("calls signOut when Sign Out button is clicked", async () => {
    const signOut = vi.fn().mockResolvedValue(undefined);
    mockUseAuth.mockReturnValue(buildAuthState({ signOut }));

    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));
    fireEvent.click(screen.getByTestId("user-menu-sign-out"));

    await waitFor(() => {
      expect(signOut).toHaveBeenCalledOnce();
    });
  });

  it("disables Sign Out button while signing out", async () => {
    // signOut resolves only after we advance; keep it pending so we can observe disabled state
    let resolveFn!: () => void;
    const signOut = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveFn = resolve;
        }),
    );
    mockUseAuth.mockReturnValue(buildAuthState({ signOut }));

    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));

    act(() => {
      fireEvent.click(screen.getByTestId("user-menu-sign-out"));
    });

    // While the promise is pending the button should be disabled
    await waitFor(() => {
      expect(screen.getByTestId("user-menu-sign-out")).toBeDisabled();
    });

    // Resolve so React doesn't leave pending state
    await act(async () => {
      resolveFn();
    });
  });

  it("shows 'Signing out...' text while signing out", async () => {
    let resolveFn!: () => void;
    const signOut = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveFn = resolve;
        }),
    );
    mockUseAuth.mockReturnValue(buildAuthState({ signOut }));

    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));

    act(() => {
      fireEvent.click(screen.getByTestId("user-menu-sign-out"));
    });

    await waitFor(() => {
      expect(screen.getByTestId("user-menu-sign-out")).toHaveTextContent(
        "Signing out...",
      );
    });

    await act(async () => {
      resolveFn();
    });
  });

  it("closes dropdown after sign out completes", async () => {
    const signOut = vi.fn().mockResolvedValue(undefined);
    mockUseAuth.mockReturnValue(buildAuthState({ signOut }));

    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));

    await act(async () => {
      fireEvent.click(screen.getByTestId("user-menu-sign-out"));
    });

    await waitFor(() => {
      expect(
        screen.queryByTestId("user-menu-dropdown"),
      ).not.toBeInTheDocument();
    });
  });

  it("closes dropdown after sign out even when signOut throws", async () => {
    const signOut = vi.fn().mockRejectedValue(new Error("network error"));
    mockUseAuth.mockReturnValue(buildAuthState({ signOut }));

    render(<UserMenu />);
    fireEvent.click(screen.getByTestId("user-menu-trigger"));

    await act(async () => {
      fireEvent.click(screen.getByTestId("user-menu-sign-out"));
    });

    await waitFor(() => {
      expect(
        screen.queryByTestId("user-menu-dropdown"),
      ).not.toBeInTheDocument();
    });
  });
});

// ---------------------------------------------------------------------------
// Avatar initial letter edge cases
// ---------------------------------------------------------------------------

describe("UserMenu — avatar initial", () => {
  it("uses first letter of name as initial", () => {
    mockUseAuth.mockReturnValue(
      buildAuthState({ user: { ...DEFAULT_USER, name: "Bob" } }),
    );
    render(<UserMenu />);
    const trigger = screen.getByTestId("user-menu-trigger");
    expect(trigger).toHaveTextContent("B");
  });

  it("uses first letter of email as initial when name is empty", () => {
    mockUseAuth.mockReturnValue(
      buildAuthState({
        user: { id: "u3", name: "", email: "carol@example.com" },
      }),
    );
    render(<UserMenu />);
    const trigger = screen.getByTestId("user-menu-trigger");
    expect(trigger).toHaveTextContent("C");
  });

  it("resets image error state when user image changes", () => {
    // Start with broken image
    mockUseAuth.mockReturnValue(
      buildAuthState({
        user: { ...DEFAULT_USER, image: "https://example.com/broken.png" },
      }),
    );
    const { rerender, container } = render(<UserMenu />);

    const img = container.querySelector("img");
    expect(img).toBeInTheDocument();
    act(() => {
      fireEvent.error(img!);
    });

    // Image should be gone (showing initial letter instead)
    expect(container.querySelector("img")).not.toBeInTheDocument();

    // Update to a new (valid) image URL — error state should reset
    mockUseAuth.mockReturnValue(
      buildAuthState({
        user: { ...DEFAULT_USER, image: "https://example.com/new-avatar.png" },
      }),
    );
    rerender(<UserMenu />);

    const newImg = container.querySelector("img");
    expect(newImg).toBeInTheDocument();
    expect(newImg).toHaveAttribute("src", "https://example.com/new-avatar.png");
  });
});
