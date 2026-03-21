/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

import type { FileReadData } from "@/api/files";

import { FileViewer } from "../FileViewer";

// ---- Mocks ----

vi.mock("@/components/CodeMirrorEditor", () => ({
  CodeMirrorEditor: ({
    value,
    language,
  }: {
    value: string;
    language?: string;
  }) => (
    <div data-testid="mock-codemirror" data-language={language}>
      {value}
    </div>
  ),
}));

// ---- Tests ----

describe("FileViewer", () => {
  const defaultProps = {
    isOpen: false,
    path: null as string | null,
    fileData: null as FileReadData | null,
    isLoading: false,
    error: null as string | null,
    onClose: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders overlay as aria-hidden when closed", () => {
    const { container } = render(<FileViewer {...defaultProps} />);
    const overlay = container.firstChild as HTMLElement;
    expect(overlay).toHaveAttribute("aria-hidden", "true");
  });

  it("renders overlay as visible when open", () => {
    const { container } = render(
      <FileViewer {...defaultProps} isOpen={true} path="main.go" />,
    );
    const overlay = container.firstChild as HTMLElement;
    expect(overlay).toHaveAttribute("aria-hidden", "false");
  });

  it("renders dialog with correct aria-label from file name", () => {
    render(
      <FileViewer {...defaultProps} isOpen={true} path="src/handler.go" />,
    );
    expect(
      screen.getByRole("dialog", { name: "File: handler.go" }),
    ).toBeInTheDocument();
  });

  it("displays full file path in header", () => {
    render(
      <FileViewer {...defaultProps} isOpen={true} path="src/internal/api.go" />,
    );
    expect(screen.getByText("src/internal/api.go")).toBeInTheDocument();
  });

  it("shows loading state", () => {
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        isLoading={true}
      />,
    );
    expect(screen.getByText("Loading file...")).toBeInTheDocument();
  });

  it("shows error message", () => {
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        error="Permission denied"
      />,
    );
    expect(screen.getByText("Permission denied")).toBeInTheDocument();
  });

  it("shows binary file notice", () => {
    const binaryData: FileReadData = {
      path: "image.png",
      size: 4096,
      binary: true,
    };
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="image.png"
        fileData={binaryData}
      />,
    );
    expect(screen.getByText(/Binary file.*cannot display/)).toBeInTheDocument();
  });

  it("renders CodeMirrorEditor for text files", async () => {
    const textData: FileReadData = {
      path: "main.go",
      content: "package main",
      size: 12,
      binary: false,
    };
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        fileData={textData}
      />,
    );
    const editor = await screen.findByTestId("mock-codemirror");
    expect(editor).toHaveTextContent("package main");
  });

  it("detects Go language for .go files", async () => {
    const goData: FileReadData = {
      path: "main.go",
      content: "package main",
      size: 12,
      binary: false,
    };
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        fileData={goData}
      />,
    );
    const editor = await screen.findByTestId("mock-codemirror");
    expect(editor).toHaveAttribute("data-language", "go");
  });

  it("detects JSON language for .json files", async () => {
    const jsonData: FileReadData = {
      path: "config.json",
      content: "{}",
      size: 2,
      binary: false,
    };
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="config.json"
        fileData={jsonData}
      />,
    );
    const editor = await screen.findByTestId("mock-codemirror");
    expect(editor).toHaveAttribute("data-language", "json");
  });

  it("detects YAML language for .yaml files", async () => {
    const yamlData: FileReadData = {
      path: "config.yaml",
      content: "key: value",
      size: 10,
      binary: false,
    };
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="config.yaml"
        fileData={yamlData}
      />,
    );
    const editor = await screen.findByTestId("mock-codemirror");
    expect(editor).toHaveAttribute("data-language", "yaml");
  });

  it("detects Markdown language for .md files", async () => {
    const mdData: FileReadData = {
      path: "README.md",
      content: "# Title",
      size: 7,
      binary: false,
    };
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="README.md"
        fileData={mdData}
      />,
    );
    const editor = await screen.findByTestId("mock-codemirror");
    expect(editor).toHaveAttribute("data-language", "markdown");
  });

  it("clicking overlay background calls onClose", () => {
    const onClose = vi.fn();
    const { container } = render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        onClose={onClose}
      />,
    );
    const overlay = container.firstChild as HTMLElement;
    fireEvent.click(overlay);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("clicking inside the panel does not call onClose", () => {
    const onClose = vi.fn();
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        onClose={onClose}
      />,
    );
    const dialog = screen.getByRole("dialog");
    fireEvent.click(dialog);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("close button calls onClose", () => {
    const onClose = vi.fn();
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="main.go"
        onClose={onClose}
      />,
    );
    fireEvent.click(screen.getByLabelText("Close file viewer"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("uses empty string for content when fileData.content is undefined", async () => {
    const noContentData: FileReadData = {
      path: "empty.go",
      size: 0,
      binary: false,
    };
    render(
      <FileViewer
        {...defaultProps}
        isOpen={true}
        path="empty.go"
        fileData={noContentData}
      />,
    );
    const editor = await screen.findByTestId("mock-codemirror");
    expect(editor).toHaveTextContent("");
  });
});
