/**
 * SearchInput component.
 * A reusable search input with icon and clear button.
 */

import {
  forwardRef,
  useCallback,
  useId,
  useState,
  type ChangeEvent,
  type KeyboardEvent,
} from 'react';

import { ClearIcon } from './ClearIcon';
import { SearchIcon } from './SearchIcon';
import styles from './SearchInput.module.css';

/**
 * Props for the SearchInput component.
 */
export interface SearchInputProps {
  /** Controlled value */
  value?: string;
  /** Default value for uncontrolled mode */
  defaultValue?: string;
  /** Called when value changes */
  onChange?: (value: string) => void;
  /** Called when clear button is clicked or Escape is pressed */
  onClear?: () => void;
  /** Placeholder text */
  placeholder?: string;
  /** Additional CSS class name */
  className?: string;
  /** Disable the input */
  disabled?: boolean;
  /** Auto-focus on mount */
  autoFocus?: boolean;
  /** Size variant */
  size?: 'sm' | 'md' | 'lg';
  /** Accessible label */
  'aria-label'?: string;
  /** Element ID */
  id?: string;
}

/**
 * SearchInput renders a search input field with icon and clear button.
 * Supports both controlled and uncontrolled modes.
 */
export const SearchInput = forwardRef<HTMLInputElement, SearchInputProps>(function SearchInput(
  {
    value: controlledValue,
    defaultValue = '',
    onChange,
    onClear,
    placeholder = 'Search...',
    className,
    disabled = false,
    autoFocus = false,
    size = 'md',
    'aria-label': ariaLabel,
    id: providedId,
  },
  ref
) {
  const generatedId = useId();
  const inputId = providedId ?? generatedId;

  // Internal state for uncontrolled mode
  const [internalValue, setInternalValue] = useState(defaultValue);

  // Determine if controlled
  const isControlled = controlledValue !== undefined;
  const currentValue = isControlled ? controlledValue : internalValue;
  const hasValue = currentValue.length > 0;

  const handleChange = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      const newValue = event.target.value;

      if (!isControlled) {
        setInternalValue(newValue);
      }

      onChange?.(newValue);
    },
    [isControlled, onChange]
  );

  const handleClear = useCallback(() => {
    if (!isControlled) {
      setInternalValue('');
    }

    onChange?.('');
    onClear?.();
  }, [isControlled, onChange, onClear]);

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLInputElement>) => {
      if (event.key === 'Escape' && hasValue) {
        event.preventDefault();
        handleClear();
      }
    },
    [hasValue, handleClear]
  );

  const iconSize = size === 'sm' ? 14 : size === 'lg' ? 18 : 16;

  const rootClassName = [styles.searchInput, styles[size], hasValue && styles.hasValue, className]
    .filter(Boolean)
    .join(' ');

  return (
    <div
      className={rootClassName}
      data-testid="search-input"
      data-size={size}
      data-has-value={hasValue || undefined}
    >
      <SearchIcon size={iconSize} className={styles.icon} />
      <input
        ref={ref}
        id={inputId}
        type="search"
        className={styles.input}
        value={currentValue}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        disabled={disabled}
        autoFocus={autoFocus}
        aria-label={ariaLabel ?? placeholder}
        data-testid="search-input-field"
      />
      {hasValue && !disabled && (
        <button
          type="button"
          className={styles.clear}
          onClick={handleClear}
          aria-label="Clear search"
          data-testid="search-input-clear"
        >
          <ClearIcon size={14} />
        </button>
      )}
    </div>
  );
});
