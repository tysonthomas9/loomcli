/**
 * DependencySection component.
 * Editable dependencies section for the Issue Detail Panel.
 * Allows users to add and remove blocking dependencies.
 */

import { useState, useCallback, useRef, useEffect, type KeyboardEvent } from 'react';
import type { IssueWithDependencyMetadata, DependencyType } from '@/types';
import styles from './DependencySection.module.css';

/**
 * Props for the DependencySection component.
 */
export interface DependencySectionProps {
  /** Current issue ID (to add dependencies to) */
  issueId: string;
  /** List of current dependencies */
  dependencies: IssueWithDependencyMetadata[];
  /** Callback when dependency is added */
  onAddDependency: (dependsOnId: string, type: DependencyType) => Promise<void>;
  /** Callback when dependency is removed */
  onRemoveDependency: (dependsOnId: string) => Promise<void>;
  /** Whether the section is read-only */
  disabled?: boolean;
  /** Custom class name */
  className?: string;
}

/**
 * DependencySection displays and manages issue dependencies.
 *
 * Features:
 * - Display existing dependencies with remove buttons
 * - Add dependency via text input (issue ID)
 * - Loading states during add/remove operations
 * - Error message display
 */
export function DependencySection({
  issueId,
  dependencies,
  onAddDependency,
  onRemoveDependency,
  disabled = false,
  className,
}: DependencySectionProps): JSX.Element {
  const [isAdding, setIsAdding] = useState(false);
  const [inputValue, setInputValue] = useState('');
  const [savingId, setSavingId] = useState<string | null>(null);
  const [removingId, setRemovingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus input when add mode is activated
  useEffect(() => {
    if (isAdding && inputRef.current) {
      inputRef.current.focus();
    }
  }, [isAdding]);

  const handleStartAdd = useCallback(() => {
    if (disabled) return;
    setIsAdding(true);
    setError(null);
  }, [disabled]);

  const handleCancelAdd = useCallback(() => {
    setIsAdding(false);
    setInputValue('');
    setError(null);
  }, []);

  const handleAdd = useCallback(async () => {
    const trimmedId = inputValue.trim();

    if (!trimmedId) {
      setError('Please enter an issue ID');
      return;
    }

    // Prevent self-dependency
    if (trimmedId === issueId) {
      setError('Cannot add self as dependency');
      return;
    }

    // Check if already a dependency
    if (dependencies.some(dep => dep.id === trimmedId)) {
      setError('Already a dependency');
      return;
    }

    setError(null);
    setSavingId(trimmedId);

    try {
      await onAddDependency(trimmedId, 'blocks');
      // Success - reset form
      setIsAdding(false);
      setInputValue('');
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to add dependency';
      setError(message);
    } finally {
      setSavingId(null);
    }
  }, [inputValue, issueId, dependencies, onAddDependency]);

  const handleRemove = useCallback(async (depId: string) => {
    if (disabled || removingId) return;

    setError(null);
    setRemovingId(depId);

    try {
      await onRemoveDependency(depId);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to remove dependency';
      setError(message);
    } finally {
      setRemovingId(null);
    }
  }, [disabled, removingId, onRemoveDependency]);

  const handleKeyDown = useCallback((e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      handleAdd();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      handleCancelAdd();
    }
  }, [handleAdd, handleCancelAdd]);

  const rootClassName = [styles.dependencySection, className].filter(Boolean).join(' ');
  const isBusy = savingId !== null || removingId !== null;

  return (
    <section className={rootClassName} data-testid="dependency-section">
      {/* Header */}
      <div className={styles.header}>
        <h3 className={styles.sectionTitle}>
          Blocked By {dependencies.length > 0 && `(${dependencies.length})`}
        </h3>
        {!disabled && !isAdding && (
          <button
            type="button"
            className={styles.addButton}
            onClick={handleStartAdd}
            disabled={isBusy}
            aria-label="Add dependency"
            data-testid="add-dependency-button"
          >
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
              <path
                d="M7 2V12M2 7H12"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
            Add
          </button>
        )}
      </div>

      {/* Error display */}
      {error && (
        <div className={styles.error} role="alert" data-testid="dependency-error">
          {error}
        </div>
      )}

      {/* Add input */}
      {isAdding && (
        <div className={styles.addForm} data-testid="add-dependency-form">
          <input
            ref={inputRef}
            type="text"
            className={styles.input}
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Enter issue ID (e.g., beads-abc1)"
            disabled={savingId !== null}
            aria-label="Issue ID"
            data-testid="dependency-input"
          />
          <div className={styles.formActions}>
            <button
              type="button"
              className={styles.cancelButton}
              onClick={handleCancelAdd}
              disabled={savingId !== null}
              data-testid="cancel-add-dependency"
            >
              Cancel
            </button>
            <button
              type="button"
              className={styles.confirmButton}
              onClick={handleAdd}
              disabled={savingId !== null || !inputValue.trim()}
              data-testid="confirm-add-dependency"
            >
              {savingId !== null ? 'Adding...' : 'Add'}
            </button>
          </div>
        </div>
      )}

      {/* Dependency list */}
      {dependencies.length > 0 ? (
        <ul className={styles.dependencyList} data-testid="dependency-list">
          {dependencies.map((dep) => {
            const statusClass = dep.status === 'closed' ? styles.dependencyClosed : '';
            const isRemoving = removingId === dep.id;

            return (
              <li
                key={dep.id}
                className={`${styles.dependencyItem} ${statusClass} ${isRemoving ? styles.removing : ''}`}
                data-testid={`dependency-item-${dep.id}`}
              >
                <span className={styles.dependencyId}>{dep.id}</span>
                <span className={styles.dependencyTitle}>{dep.title}</span>
                {dep.dependency_type && (
                  <span className={styles.dependencyType}>{dep.dependency_type}</span>
                )}
                {!disabled && (
                  <button
                    type="button"
                    className={styles.removeButton}
                    onClick={() => handleRemove(dep.id)}
                    disabled={isBusy}
                    aria-label={`Remove dependency ${dep.id}`}
                    data-testid={`remove-dependency-${dep.id}`}
                  >
                    {isRemoving ? (
                      <span className={styles.spinner} />
                    ) : (
                      <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
                        <path
                          d="M3 3L11 11M11 3L3 11"
                          stroke="currentColor"
                          strokeWidth="2"
                          strokeLinecap="round"
                        />
                      </svg>
                    )}
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      ) : (
        !isAdding && (
          <p className={styles.emptyMessage} data-testid="no-dependencies">
            No blocking dependencies
          </p>
        )
      )}
    </section>
  );
}
