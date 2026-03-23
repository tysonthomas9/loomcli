import { test, expect } from "@playwright/test";

/**
 * E2E tests for SessionNamePrompt component.
 * Uses the /test/session-name-prompt fixture route which renders the prompt
 * in isolation with interactive controls for observing callbacks.
 *
 * The component already has 30+ unit tests covering validation logic;
 * these E2E tests focus on browser-level behaviors: real DOM focus,
 * CSS rendering, keyboard form submission, and ARIA attributes.
 */

const FIXTURE_URL = "/test/session-name-prompt";

test.describe("SessionNamePrompt", () => {
  test.describe("Rendering", () => {
    test("modal displays with title and subtitle", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await expect(
        page.getByTestId("session-name-prompt-modal"),
      ).toBeVisible();
      await expect(page.getByText("New Terminal Session")).toBeVisible();
      await expect(
        page.getByText("Enter a name for the new session"),
      ).toBeVisible();
    });

    test("input has placeholder text", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await expect(page.getByTestId("session-name-input")).toHaveAttribute(
        "placeholder",
        "e.g. auth-redesign",
      );
    });

    test("Cancel and Create buttons are visible", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await expect(
        page.getByTestId("session-name-cancel-button"),
      ).toBeVisible();
      await expect(
        page.getByTestId("session-name-confirm-button"),
      ).toBeVisible();
    });
  });

  test.describe("Auto-focus", () => {
    test("input receives focus automatically", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      // The component uses setTimeout(100ms) for focus
      await expect(page.getByTestId("session-name-input")).toBeFocused({
        timeout: 500,
      });
    });
  });

  test.describe("Valid Input", () => {
    test("entering valid name enables Create button", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-input").fill("my-session");
      await expect(
        page.getByTestId("session-name-confirm-button"),
      ).toBeEnabled();
    });

    test("no error shown for valid input", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-input").fill("test_123-name");
      await expect(page.getByTestId("session-name-error")).toHaveCount(0);
    });
  });

  test.describe("Invalid Characters", () => {
    test("shows error for invalid characters", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-input").fill("bad name!");
      await expect(page.getByTestId("session-name-error")).toBeVisible();
      await expect(page.getByTestId("session-name-error")).toContainText(
        "Only letters, numbers, hyphens, and underscores are allowed",
      );
      await expect(
        page.getByTestId("session-name-confirm-button"),
      ).toBeDisabled();
    });

    test("shows error for spaces", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-input").fill("has spaces");
      await expect(
        page.getByTestId("session-name-confirm-button"),
      ).toBeDisabled();
    });
  });

  test.describe("Duplicate Name", () => {
    test("shows duplicate error when name matches existing", async ({
      page,
    }) => {
      await page.goto(`${FIXTURE_URL}?existingNames=foo`);

      await page.getByTestId("session-name-input").fill("foo");

      await expect(page.getByTestId("session-name-error")).toBeVisible();
      await expect(page.getByTestId("session-name-error")).toContainText(
        "Session already exists",
      );
      await expect(
        page.getByTestId("session-name-confirm-button"),
      ).toBeDisabled();
    });

    test("URL param existingNames also works", async ({ page }) => {
      await page.goto(`${FIXTURE_URL}?existingNames=bar`);

      await page.getByTestId("session-name-input").fill("bar");
      await expect(page.getByTestId("session-name-error")).toBeVisible();
      await expect(page.getByTestId("session-name-error")).toContainText(
        "Session already exists",
      );
    });
  });

  test.describe("Empty Input", () => {
    test("Create button disabled when empty", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await expect(
        page.getByTestId("session-name-confirm-button"),
      ).toBeDisabled();
      await expect(page.getByTestId("session-name-error")).toHaveCount(0);
    });
  });

  test.describe("Form Submission", () => {
    test("clicking Create confirms with trimmed name", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-input").fill("my-session");
      await page.getByTestId("session-name-confirm-button").click();

      await expect(page.getByTestId("confirmed-name")).toHaveText(
        "my-session",
      );
      await expect(page.getByTestId("confirm-count")).toHaveText("1");
    });

    test("whitespace is trimmed on submit", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-input").fill("  padded-name  ");
      await page.getByTestId("session-name-confirm-button").click();

      await expect(page.getByTestId("confirmed-name")).toHaveText(
        "padded-name",
      );
    });

    test("Enter key submits the form", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-input").fill("enter-test");
      await page.getByTestId("session-name-input").press("Enter");

      await expect(page.getByTestId("confirmed-name")).toHaveText(
        "enter-test",
      );
      await expect(page.getByTestId("confirm-count")).toHaveText("1");
    });

    test("invalid input does not confirm on submit", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-input").fill("bad name!");
      await page.getByTestId("session-name-input").press("Enter");

      await expect(page.getByTestId("confirm-count")).toHaveText("0");
    });
  });

  test.describe("Cancel", () => {
    test("clicking Cancel increments cancel count", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-cancel-button").click();
      await expect(page.getByTestId("cancel-count")).toHaveText("1");
    });
  });

  test.describe("Input Reset", () => {
    test("reopening clears input and error state", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await page.getByTestId("session-name-input").fill("some-value");
      // Cancel to close the prompt
      await page.getByTestId("session-name-cancel-button").click();
      // Reopen via the fixture button
      await page.getByTestId("reopen-button").click();

      // Wait for modal to reappear
      await expect(
        page.getByTestId("session-name-prompt-modal"),
      ).toBeVisible();
      await expect(page.getByTestId("session-name-input")).toHaveValue("");
      await expect(page.getByTestId("session-name-error")).toHaveCount(0);
    });
  });

  test.describe("Accessibility", () => {
    test("modal has correct ARIA attributes", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      const modal = page.getByTestId("session-name-prompt-modal");
      await expect(modal).toHaveAttribute("role", "dialog");
      await expect(modal).toHaveAttribute("aria-modal", "true");
      await expect(modal).toHaveAttribute(
        "aria-labelledby",
        "session-name-prompt-title",
      );
    });

    test("input is associated with label via htmlFor", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      const input = page.getByTestId("session-name-input");
      await expect(input).toHaveAttribute("id", "session-name");
      // The label element exists with matching htmlFor
      await expect(page.locator('label[for="session-name"]')).toBeVisible();
    });

    test("overlay has aria-hidden=false when open", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await expect(
        page.getByTestId("session-name-prompt-overlay"),
      ).toHaveAttribute("aria-hidden", "false");
    });
  });
});
