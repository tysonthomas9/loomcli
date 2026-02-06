/**
 * StatusDropdown component.
 * Interactive dropdown for changing issue status.
 */

import { useCallback } from 'react';

import { formatStatusLabel } from '@/components/StatusColumn/utils';
import type { Status, KnownStatus } from '@/types/status';
import { USER_SELECTABLE_STATUSES } from '@/types/status';

import styles from './StatusDropdown.module.css';

/**
 * Status option for the dropdown.
 */
interface StatusOption {
  /** The status value */
  value: KnownStatus;
  /** Human-readable label */
  label: string;
}

/**
 * Status options for the dropdown.
 */
const STATUS_OPTIONS: StatusOption[] = USER_SELECTABLE_STATUSES.map((status) => ({
  value: status,
  label: formatStatusLabel(status),
}));

/**
 * Props for the StatusDropdown component.
 */
export interface StatusDropdownProps {
  /** Current status value */
  status: Status;
  /** Callback when status changes */
  onStatusChange: (status: Status) => void;
  /** Whether the dropdown is disabled */
  disabled?: boolean;
  /** Whether a status change is being saved */
  isSaving?: boolean;
  /** Additional CSS class name */
  className?: string;
}

/**
 * StatusDropdown renders an interactive dropdown for changing issue status.
 * Uses native select element for accessibility and simplicity.
 * Styled to match the existing status badge appearance.
 */
export function StatusDropdown({
  status,
  onStatusChange,
  disabled,
  isSaving,
  className,
}: StatusDropdownProps): JSX.Element {
  const handleChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      const newStatus = event.target.value as Status;
      // Skip if same status selected
      if (newStatus !== status) {
        onStatusChange(newStatus);
      }
    },
    [status, onStatusChange]
  );

  const isDisabled = disabled || isSaving;
  const rootClassName = [styles.statusDropdown, className].filter(Boolean).join(' ');

  return (
    <select
      className={rootClassName}
      value={status}
      onChange={handleChange}
      disabled={isDisabled}
      data-status={status}
      data-saving={isSaving || undefined}
      aria-label="Change issue status"
      data-testid="status-dropdown"
    >
      {STATUS_OPTIONS.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  );
}
