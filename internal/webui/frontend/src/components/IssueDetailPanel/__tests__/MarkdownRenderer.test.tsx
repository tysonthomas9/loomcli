/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for MarkdownRenderer component.
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';

import '@testing-library/jest-dom';
import { MarkdownRenderer } from '../MarkdownRenderer';

describe('MarkdownRenderer', () => {
  describe('Empty states', () => {
    it('renders empty state for null content', () => {
      render(<MarkdownRenderer content={null} />);
      expect(screen.getByTestId('markdown-empty')).toBeInTheDocument();
      expect(screen.getByText('No content')).toBeInTheDocument();
    });

    it('renders empty state for undefined content', () => {
      render(<MarkdownRenderer content={undefined} />);
      expect(screen.getByTestId('markdown-empty')).toBeInTheDocument();
    });

    it('renders empty state for empty string', () => {
      render(<MarkdownRenderer content="" />);
      expect(screen.getByTestId('markdown-empty')).toBeInTheDocument();
    });
  });

  describe('Markdown rendering', () => {
    it('renders markdown content', () => {
      render(<MarkdownRenderer content="# Hello World" />);
      expect(screen.getByTestId('markdown-content')).toBeInTheDocument();
      expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Hello World');
    });

    it('renders H2 headings correctly', () => {
      render(<MarkdownRenderer content="## Summary" />);
      expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Summary');
    });

    it('renders H3 headings correctly', () => {
      render(<MarkdownRenderer content="### Details" />);
      expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Details');
    });

    it('renders multiple headings', () => {
      const content = `## Summary

### Details`;
      render(<MarkdownRenderer content={content} />);
      expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Summary');
      expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Details');
    });

    it('renders code blocks', () => {
      const content = `\`\`\`typescript
const x = 1;
\`\`\``;
      render(<MarkdownRenderer content={content} />);
      expect(screen.getByText('const x = 1;')).toBeInTheDocument();
    });

    it('renders inline code', () => {
      render(<MarkdownRenderer content="Use `npm install` to setup" />);
      const code = screen.getByText('npm install');
      expect(code.tagName.toLowerCase()).toBe('code');
    });

    it('renders unordered lists', () => {
      const content = `- Item 1
- Item 2`;
      render(<MarkdownRenderer content={content} />);
      const items = screen.getAllByRole('listitem');
      expect(items).toHaveLength(2);
      expect(items[0]).toHaveTextContent('Item 1');
      expect(items[1]).toHaveTextContent('Item 2');
    });

    it('renders ordered lists', () => {
      const content = `1. First
2. Second`;
      render(<MarkdownRenderer content={content} />);
      const items = screen.getAllByRole('listitem');
      expect(items).toHaveLength(2);
      expect(items[0]).toHaveTextContent('First');
      expect(items[1]).toHaveTextContent('Second');
    });

    it('renders links', () => {
      render(<MarkdownRenderer content="[Click here](https://example.com)" />);
      const link = screen.getByRole('link', { name: 'Click here' });
      expect(link).toHaveAttribute('href', 'https://example.com');
    });

    it('renders bold text', () => {
      render(<MarkdownRenderer content="This is **bold** text" />);
      const strong = screen.getByText('bold');
      expect(strong.tagName.toLowerCase()).toBe('strong');
    });

    it('renders italic text', () => {
      render(<MarkdownRenderer content="This is *italic* text" />);
      const em = screen.getByText('italic');
      expect(em.tagName.toLowerCase()).toBe('em');
    });

    // Tables require remark-gfm plugin which is not currently configured
    it.skip('renders tables', () => {
      const content = `| Col1 | Col2 |
|------|------|
| A | B |`;
      render(<MarkdownRenderer content={content} />);
      expect(screen.getByRole('table')).toBeInTheDocument();
      expect(screen.getByText('A')).toBeInTheDocument();
      expect(screen.getByText('B')).toBeInTheDocument();
    });

    it('renders paragraphs', () => {
      const content = `First paragraph

Second paragraph`;
      render(<MarkdownRenderer content={content} />);
      expect(screen.getByText('First paragraph')).toBeInTheDocument();
      expect(screen.getByText('Second paragraph')).toBeInTheDocument();
    });
  });

  describe('Custom className', () => {
    it('applies custom className to content', () => {
      render(<MarkdownRenderer content="Test" className="custom-class" />);
      const container = screen.getByTestId('markdown-content');
      expect(container).toHaveClass('custom-class');
    });

    it('applies custom className to empty state', () => {
      render(<MarkdownRenderer content={null} className="custom-class" />);
      const container = screen.getByTestId('markdown-empty');
      expect(container).toHaveClass('custom-class');
    });
  });
});
