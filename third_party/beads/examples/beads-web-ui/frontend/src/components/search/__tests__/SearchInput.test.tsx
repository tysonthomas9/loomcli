/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SearchInput component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { createRef } from 'react';
import { describe, it, expect, vi } from 'vitest';
import '@testing-library/jest-dom';

import { SearchInput } from '../SearchInput';

describe('SearchInput', () => {
  describe('rendering', () => {
    it('renders with default props', () => {
      render(<SearchInput />);

      expect(screen.getByTestId('search-input')).toBeInTheDocument();
      expect(screen.getByTestId('search-input-field')).toBeInTheDocument();
    });

    it('renders with placeholder text', () => {
      render(<SearchInput placeholder="Search issues..." />);

      expect(screen.getByPlaceholderText('Search issues...')).toBeInTheDocument();
    });

    it('renders with default placeholder when none provided', () => {
      render(<SearchInput />);

      expect(screen.getByPlaceholderText('Search...')).toBeInTheDocument();
    });

    it('renders with custom className', () => {
      render(<SearchInput className="custom-class" />);

      const root = screen.getByTestId('search-input');
      expect(root).toHaveClass('custom-class');
    });

    it('renders search icon', () => {
      const { container } = render(<SearchInput />);

      const icon = container.querySelector('svg');
      expect(icon).toBeInTheDocument();
    });
  });

  describe('size variants', () => {
    it('applies sm size', () => {
      render(<SearchInput size="sm" />);

      const root = screen.getByTestId('search-input');
      expect(root).toHaveAttribute('data-size', 'sm');
    });

    it('applies md size by default', () => {
      render(<SearchInput />);

      const root = screen.getByTestId('search-input');
      expect(root).toHaveAttribute('data-size', 'md');
    });

    it('applies lg size', () => {
      render(<SearchInput size="lg" />);

      const root = screen.getByTestId('search-input');
      expect(root).toHaveAttribute('data-size', 'lg');
    });

    it('applies all size variants correctly', () => {
      const sizes = ['sm', 'md', 'lg'] as const;

      sizes.forEach((size) => {
        const { unmount } = render(<SearchInput size={size} />);

        const root = screen.getByTestId('search-input');
        expect(root).toHaveAttribute('data-size', size);

        unmount();
      });
    });
  });

  describe('controlled mode', () => {
    it('reflects value prop in input', () => {
      render(<SearchInput value="test query" onChange={() => {}} />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue('test query');
    });

    it('updates when value prop changes', () => {
      const { rerender } = render(<SearchInput value="initial" onChange={() => {}} />);

      expect(screen.getByTestId('search-input-field')).toHaveValue('initial');

      rerender(<SearchInput value="updated" onChange={() => {}} />);

      expect(screen.getByTestId('search-input-field')).toHaveValue('updated');
    });

    it('does not update internal state when controlled', () => {
      const onChange = vi.fn();
      render(<SearchInput value="controlled" onChange={onChange} />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.change(input, { target: { value: 'controlledx' } });

      // Value should still be controlled value since onChange doesn't update it
      expect(input).toHaveValue('controlled');
      expect(onChange).toHaveBeenCalled();
    });
  });

  describe('uncontrolled mode', () => {
    it('uses defaultValue as initial value', () => {
      render(<SearchInput defaultValue="default query" />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue('default query');
    });

    it('updates internal state on input', () => {
      render(<SearchInput defaultValue="" />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.change(input, { target: { value: 'typed value' } });

      expect(input).toHaveValue('typed value');
    });

    it('starts with empty string when no defaultValue', () => {
      render(<SearchInput />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue('');
    });
  });

  describe('onChange callback', () => {
    it('fires on input change', () => {
      const handleChange = vi.fn();
      render(<SearchInput onChange={handleChange} />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.change(input, { target: { value: 'a' } });

      expect(handleChange).toHaveBeenCalledWith('a');
    });

    it('fires with correct value on each change', () => {
      const handleChange = vi.fn();
      render(<SearchInput onChange={handleChange} />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.change(input, { target: { value: 'a' } });
      fireEvent.change(input, { target: { value: 'ab' } });
      fireEvent.change(input, { target: { value: 'abc' } });

      expect(handleChange).toHaveBeenCalledTimes(3);
      expect(handleChange).toHaveBeenNthCalledWith(1, 'a');
      expect(handleChange).toHaveBeenNthCalledWith(2, 'ab');
      expect(handleChange).toHaveBeenNthCalledWith(3, 'abc');
    });

    it('fires in controlled mode', () => {
      const handleChange = vi.fn();
      render(<SearchInput value="" onChange={handleChange} />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.change(input, { target: { value: 'new value' } });

      expect(handleChange).toHaveBeenCalledWith('new value');
    });
  });

  describe('clear button', () => {
    it('appears when value is non-empty', () => {
      render(<SearchInput value="query" onChange={() => {}} />);

      expect(screen.getByTestId('search-input-clear')).toBeInTheDocument();
    });

    it('hides when value is empty', () => {
      render(<SearchInput value="" onChange={() => {}} />);

      expect(screen.queryByTestId('search-input-clear')).not.toBeInTheDocument();
    });

    it('hides when input is empty in uncontrolled mode', () => {
      render(<SearchInput />);

      expect(screen.queryByTestId('search-input-clear')).not.toBeInTheDocument();
    });

    it('appears when typing in uncontrolled mode', () => {
      render(<SearchInput />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.change(input, { target: { value: 'x' } });

      expect(screen.getByTestId('search-input-clear')).toBeInTheDocument();
    });

    it('is hidden when disabled even with value', () => {
      render(<SearchInput value="query" onChange={() => {}} disabled />);

      expect(screen.queryByTestId('search-input-clear')).not.toBeInTheDocument();
    });

    it('has data-has-value attribute when value present', () => {
      render(<SearchInput value="query" onChange={() => {}} />);

      const root = screen.getByTestId('search-input');
      expect(root).toHaveAttribute('data-has-value', 'true');
    });

    it('does not have data-has-value attribute when empty', () => {
      render(<SearchInput value="" onChange={() => {}} />);

      const root = screen.getByTestId('search-input');
      expect(root).not.toHaveAttribute('data-has-value');
    });
  });

  describe('onClear callback', () => {
    it('fires when clear button is clicked', () => {
      const handleClear = vi.fn();
      render(<SearchInput value="query" onChange={() => {}} onClear={handleClear} />);

      const clearButton = screen.getByTestId('search-input-clear');
      fireEvent.click(clearButton);

      expect(handleClear).toHaveBeenCalledTimes(1);
    });

    it('calls onChange with empty string when cleared', () => {
      const handleChange = vi.fn();
      render(<SearchInput value="query" onChange={handleChange} />);

      const clearButton = screen.getByTestId('search-input-clear');
      fireEvent.click(clearButton);

      expect(handleChange).toHaveBeenCalledWith('');
    });

    it('clears internal state in uncontrolled mode', () => {
      const handleClear = vi.fn();
      render(<SearchInput defaultValue="query" onClear={handleClear} />);

      const clearButton = screen.getByTestId('search-input-clear');
      fireEvent.click(clearButton);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue('');
      expect(handleClear).toHaveBeenCalled();
    });
  });

  describe('Escape key', () => {
    it('clears input when Escape is pressed', () => {
      const handleClear = vi.fn();
      render(<SearchInput defaultValue="query" onClear={handleClear} />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.keyDown(input, { key: 'Escape' });

      expect(input).toHaveValue('');
      expect(handleClear).toHaveBeenCalled();
    });

    it('calls onChange with empty string on Escape', () => {
      const handleChange = vi.fn();
      render(<SearchInput value="query" onChange={handleChange} />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.keyDown(input, { key: 'Escape' });

      expect(handleChange).toHaveBeenCalledWith('');
    });

    it('does not clear when input is already empty', () => {
      const handleClear = vi.fn();
      render(<SearchInput value="" onChange={() => {}} onClear={handleClear} />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.keyDown(input, { key: 'Escape' });

      expect(handleClear).not.toHaveBeenCalled();
    });

    it('prevents default on Escape when value exists', () => {
      render(<SearchInput defaultValue="query" />);

      const input = screen.getByTestId('search-input-field');
      const event = new KeyboardEvent('keydown', {
        key: 'Escape',
        bubbles: true,
        cancelable: true,
      });

      const preventDefaultSpy = vi.spyOn(event, 'preventDefault');
      input.dispatchEvent(event);

      expect(preventDefaultSpy).toHaveBeenCalled();
    });

    it('does not call onClear on non-Escape keys', () => {
      const handleClear = vi.fn();
      render(<SearchInput defaultValue="query" onClear={handleClear} />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.keyDown(input, { key: 'Enter' });
      fireEvent.keyDown(input, { key: 'Tab' });
      fireEvent.keyDown(input, { key: 'a' });

      expect(handleClear).not.toHaveBeenCalled();
    });
  });

  describe('disabled state', () => {
    it('disables the input element', () => {
      render(<SearchInput disabled />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toBeDisabled();
    });

    it('does not show clear button when disabled', () => {
      render(<SearchInput disabled value="query" onChange={() => {}} />);

      expect(screen.queryByTestId('search-input-clear')).not.toBeInTheDocument();
    });

    it('can still display a value when disabled', () => {
      render(<SearchInput disabled value="readonly value" onChange={() => {}} />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue('readonly value');
    });

    it('is not disabled by default', () => {
      render(<SearchInput />);

      const input = screen.getByTestId('search-input-field');
      expect(input).not.toBeDisabled();
    });
  });

  describe('ref forwarding', () => {
    it('forwards ref to input element', () => {
      const ref = createRef<HTMLInputElement>();
      render(<SearchInput ref={ref} />);

      expect(ref.current).toBeInstanceOf(HTMLInputElement);
      expect(ref.current).toBe(screen.getByTestId('search-input-field'));
    });

    it('allows focus via ref', () => {
      const ref = createRef<HTMLInputElement>();
      render(<SearchInput ref={ref} />);

      ref.current?.focus();

      expect(document.activeElement).toBe(ref.current);
    });

    it('allows blur via ref', () => {
      const ref = createRef<HTMLInputElement>();
      render(<SearchInput ref={ref} />);

      ref.current?.focus();
      ref.current?.blur();

      expect(document.activeElement).not.toBe(ref.current);
    });

    it('allows value manipulation via ref', () => {
      const ref = createRef<HTMLInputElement>();
      render(<SearchInput ref={ref} />);

      expect(ref.current?.value).toBe('');
    });
  });

  describe('accessibility', () => {
    it('sets aria-label from prop', () => {
      render(<SearchInput aria-label="Search all items" />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveAttribute('aria-label', 'Search all items');
    });

    it('uses placeholder as aria-label when not provided', () => {
      render(<SearchInput placeholder="Find issues" />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveAttribute('aria-label', 'Find issues');
    });

    it('uses default placeholder as aria-label when neither provided', () => {
      render(<SearchInput />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveAttribute('aria-label', 'Search...');
    });

    it('clear button has accessible label', () => {
      render(<SearchInput value="query" onChange={() => {}} />);

      const clearButton = screen.getByRole('button', { name: 'Clear search' });
      expect(clearButton).toBeInTheDocument();
    });

    it('input has type="search"', () => {
      render(<SearchInput />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveAttribute('type', 'search');
    });

    it('custom aria-label overrides placeholder', () => {
      render(<SearchInput placeholder="Search..." aria-label="Custom search label" />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveAttribute('aria-label', 'Custom search label');
    });

    it('can find input by role', () => {
      render(<SearchInput aria-label="Search field" />);

      expect(screen.getByRole('searchbox', { name: 'Search field' })).toBeInTheDocument();
    });
  });

  describe('id prop', () => {
    it('uses provided id', () => {
      render(<SearchInput id="my-search" />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveAttribute('id', 'my-search');
    });

    it('generates id when not provided', () => {
      render(<SearchInput />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveAttribute('id');
      expect(input.id).not.toBe('');
    });

    it('each instance gets unique generated id', () => {
      render(
        <>
          <SearchInput />
          <SearchInput />
        </>
      );

      const inputs = screen.getAllByTestId('search-input-field');
      expect(inputs[0]?.id).not.toBe(inputs[1]?.id);
    });
  });

  describe('autoFocus', () => {
    it('focuses input on mount when autoFocus is true', () => {
      render(<SearchInput autoFocus />);

      const input = screen.getByTestId('search-input-field');
      expect(document.activeElement).toBe(input);
    });

    it('does not focus input on mount when autoFocus is false', () => {
      render(<SearchInput autoFocus={false} />);

      const input = screen.getByTestId('search-input-field');
      expect(document.activeElement).not.toBe(input);
    });

    it('does not focus by default', () => {
      render(<SearchInput />);

      const input = screen.getByTestId('search-input-field');
      expect(document.activeElement).not.toBe(input);
    });
  });

  describe('edge cases', () => {
    it('handles rapid value changes', () => {
      const handleChange = vi.fn();
      const { rerender } = render(<SearchInput value="" onChange={handleChange} />);

      rerender(<SearchInput value="a" onChange={handleChange} />);
      rerender(<SearchInput value="ab" onChange={handleChange} />);
      rerender(<SearchInput value="abc" onChange={handleChange} />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue('abc');
    });

    it('handles switching from uncontrolled to controlled', () => {
      const { rerender } = render(<SearchInput defaultValue="uncontrolled" />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue('uncontrolled');

      rerender(<SearchInput value="controlled" onChange={() => {}} />);
      expect(input).toHaveValue('controlled');
    });

    it('handles empty string value correctly', () => {
      render(<SearchInput value="" onChange={() => {}} />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue('');
      expect(screen.queryByTestId('search-input-clear')).not.toBeInTheDocument();
    });

    it('handles special characters in value', () => {
      render(<SearchInput value="<script>alert(1)</script>" onChange={() => {}} />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue('<script>alert(1)</script>');
    });

    it('handles unicode characters', () => {
      render(<SearchInput defaultValue="" />);

      const input = screen.getByTestId('search-input-field');
      fireEvent.change(input, { target: { value: '日本語 ' } });

      expect(input).toHaveValue('日本語 ');
    });

    it('combines multiple class names correctly', () => {
      render(<SearchInput size="lg" className="extra-class" value="x" onChange={() => {}} />);

      const root = screen.getByTestId('search-input');
      expect(root).toHaveAttribute('data-size', 'lg');
      expect(root).toHaveAttribute('data-has-value', 'true');
      expect(root).toHaveClass('extra-class');
    });

    it('handles whitespace-only value as having value', () => {
      render(<SearchInput value="   " onChange={() => {}} />);

      const root = screen.getByTestId('search-input');
      expect(root).toHaveAttribute('data-has-value', 'true');
      expect(screen.getByTestId('search-input-clear')).toBeInTheDocument();
    });

    it('handles very long values', () => {
      const longValue = 'a'.repeat(1000);
      render(<SearchInput value={longValue} onChange={() => {}} />);

      const input = screen.getByTestId('search-input-field');
      expect(input).toHaveValue(longValue);
    });
  });

  describe('CSS width constraints', () => {
    it('has searchInput CSS module class applied', () => {
      render(<SearchInput />);

      const container = screen.getByTestId('search-input');

      // Verify the searchInput class is applied (CSS Modules transforms it to _searchInput_<hash>)
      expect(container.className).toMatch(/searchInput/);
    });

    it('respects max-width across all size variants', () => {
      const sizes = ['sm', 'md', 'lg'] as const;

      sizes.forEach((size) => {
        const { unmount } = render(<SearchInput size={size} />);

        const container = screen.getByTestId('search-input');

        // CSS module class should be applied regardless of size
        expect(container.className).toMatch(/searchInput/);

        unmount();
      });
    });

    it('applies searchInput class with size variant classes', () => {
      render(<SearchInput size="lg" />);

      const container = screen.getByTestId('search-input');

      // Should have both searchInput class (with max-width) and size class
      expect(container.className).toMatch(/searchInput/);
      expect(container).toHaveAttribute('data-size', 'lg');
    });

    it('maintains searchInput class with custom className', () => {
      render(<SearchInput className="custom-search" />);

      const container = screen.getByTestId('search-input');

      // Should have both CSS module class and custom class
      expect(container.className).toMatch(/searchInput/);
      expect(container).toHaveClass('custom-search');
    });

    it('maintains searchInput class in disabled state', () => {
      render(<SearchInput disabled />);

      const container = screen.getByTestId('search-input');

      // CSS module class should apply regardless of disabled state
      expect(container.className).toMatch(/searchInput/);
    });

    it('maintains searchInput class with value', () => {
      render(<SearchInput value="test query" onChange={() => {}} />);

      const container = screen.getByTestId('search-input');

      // CSS module class should apply when input has value
      expect(container.className).toMatch(/searchInput/);
    });
  });
});
