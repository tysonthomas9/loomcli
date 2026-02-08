/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SettingsView component.
 *
 * These tests verify rendering of loading, error, and data states,
 * the backend dropdown, save button behavior, and agent override table.
 */

import { render, screen, fireEvent, within, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import '@testing-library/jest-dom';

import type { UseBackendConfigReturn } from '@/hooks/useBackendConfig';
import type { BackendConfigData } from '@/api/config';

import { SettingsView } from '../SettingsView';

// Mock the hooks used by SettingsView
vi.mock('@/hooks/useBackendConfig', () => ({
  useBackendConfig: vi.fn(),
}));

vi.mock('@/hooks/useToast', () => ({
  useToast: vi.fn(() => ({
    showToast: vi.fn(),
    toasts: [],
    dismissToast: vi.fn(),
    dismissAll: vi.fn(),
  })),
}));

import { useBackendConfig } from '@/hooks/useBackendConfig';
import { useToast } from '@/hooks/useToast';

const mockUseBackendConfig = vi.mocked(useBackendConfig);
const mockUseToast = vi.mocked(useToast);

/**
 * Helper to create a mock BackendConfigData.
 */
function createMockConfig(overrides?: Partial<BackendConfigData>): BackendConfigData {
  return {
    backend: 'anthropic',
    source: 'project',
    available: ['anthropic', 'openai', 'local'],
    agents: [],
    ...overrides,
  };
}

/**
 * Helper to create a mock UseBackendConfigReturn.
 */
function createMockHookReturn(
  overrides?: Partial<UseBackendConfigReturn>
): UseBackendConfigReturn {
  return {
    config: createMockConfig(),
    isLoading: false,
    error: null,
    isSaving: false,
    updateBackend: vi.fn().mockResolvedValue(undefined),
    refetch: vi.fn(),
    ...overrides,
  };
}

describe('SettingsView', () => {
  const mockShowToast = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    mockUseToast.mockReturnValue({
      showToast: mockShowToast,
      toasts: [],
      dismissToast: vi.fn(),
      dismissAll: vi.fn(),
    });
  });

  describe('loading state', () => {
    it('renders loading skeleton while fetching', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({ config: null, isLoading: true })
      );

      render(<SettingsView />);

      expect(screen.getByTestId('settings-view')).toBeInTheDocument();
      expect(screen.getByText('Settings')).toBeInTheDocument();
      expect(screen.getByText('Project Default Backend')).toBeInTheDocument();
      // LoadingSkeleton renders with aria-hidden
      const settingsView = screen.getByTestId('settings-view');
      const skeleton = settingsView.querySelector('[aria-hidden="true"]');
      expect(skeleton).toBeInTheDocument();
    });
  });

  describe('error state', () => {
    it('renders error display when fetch fails and no config', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({ config: null, isLoading: false, error: 'Server error' })
      );

      render(<SettingsView />);

      expect(screen.getByTestId('settings-view')).toBeInTheDocument();
      expect(screen.getByText('Backend configuration unavailable')).toBeInTheDocument();
    });
  });

  describe('backend dropdown', () => {
    it('renders backend dropdown with current value selected', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ backend: 'openai' }),
        })
      );

      render(<SettingsView />);

      const select = screen.getByTestId('backend-select') as HTMLSelectElement;
      expect(select).toBeInTheDocument();
      expect(select.value).toBe('openai');
    });

    it('renders available backend options in dropdown', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({
            available: ['anthropic', 'openai', 'local', 'azure'],
          }),
        })
      );

      render(<SettingsView />);

      const select = screen.getByTestId('backend-select') as HTMLSelectElement;
      const options = within(select).getAllByRole('option');

      expect(options).toHaveLength(4);
      expect(options[0]).toHaveTextContent('anthropic');
      expect(options[1]).toHaveTextContent('openai');
      expect(options[2]).toHaveTextContent('local');
      expect(options[3]).toHaveTextContent('azure');
    });

    it('renders source tag with config source', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ source: 'project' }),
        })
      );

      render(<SettingsView />);

      expect(screen.getByText('From project loom.yaml')).toBeInTheDocument();
    });

    it('renders "Default" source tag when source is not project', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ source: 'default' }),
        })
      );

      render(<SettingsView />);

      expect(screen.getByText('Default')).toBeInTheDocument();
    });
  });

  describe('save button', () => {
    it('save button disabled when no changes', () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());

      render(<SettingsView />);

      const saveButton = screen.getByTestId('save-button');
      expect(saveButton).toBeDisabled();
    });

    it('save button enabled after dropdown change', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ backend: 'anthropic' }),
        })
      );

      render(<SettingsView />);

      const select = screen.getByTestId('backend-select');
      fireEvent.change(select, { target: { value: 'openai' } });

      const saveButton = screen.getByTestId('save-button');
      expect(saveButton).not.toBeDisabled();
    });

    it('calls updateBackend on save button click', async () => {
      const mockUpdateBackend = vi.fn().mockResolvedValue(undefined);
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ backend: 'anthropic' }),
          updateBackend: mockUpdateBackend,
        })
      );

      render(<SettingsView />);

      // Change dropdown value
      const select = screen.getByTestId('backend-select');
      fireEvent.change(select, { target: { value: 'openai' } });

      // Click save
      const saveButton = screen.getByTestId('save-button');
      await act(async () => {
        fireEvent.click(saveButton);
      });

      expect(mockUpdateBackend).toHaveBeenCalledWith('openai');
    });

    it('shows "Saving..." text when isSaving is true', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({ isSaving: true })
      );

      render(<SettingsView />);

      const saveButton = screen.getByTestId('save-button');
      expect(saveButton).toHaveTextContent('Saving...');
    });

    it('save button disabled when isSaving is true', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({ isSaving: true })
      );

      render(<SettingsView />);

      const saveButton = screen.getByTestId('save-button');
      expect(saveButton).toBeDisabled();
    });
  });

  describe('agent override table', () => {
    it('renders agent override table when agents have overrides', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({
            agents: [
              { worktree: 'feature-a', role: 'coder', backend: 'openai' },
              { worktree: 'feature-b', role: 'reviewer', backend: 'local' },
            ],
          }),
        })
      );

      render(<SettingsView />);

      const table = screen.getByTestId('agent-overrides-table');
      expect(table).toBeInTheDocument();

      // Check table headers
      expect(screen.getByText('Worktree')).toBeInTheDocument();
      expect(screen.getByText('Role')).toBeInTheDocument();
      expect(screen.getByText('Backend')).toBeInTheDocument();

      // Check table data
      expect(screen.getByText('feature-a')).toBeInTheDocument();
      expect(screen.getByText('coder')).toBeInTheDocument();
      expect(screen.getByText('feature-b')).toBeInTheDocument();
      expect(screen.getByText('reviewer')).toBeInTheDocument();
    });

    it('hides agent table when no overrides', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({
            agents: [
              { worktree: 'feature-a', role: 'coder', backend: '' },
              { worktree: 'feature-b', role: 'reviewer', backend: '' },
            ],
          }),
        })
      );

      render(<SettingsView />);

      expect(screen.queryByTestId('agent-overrides-table')).not.toBeInTheDocument();
      expect(screen.getByTestId('no-overrides-message')).toBeInTheDocument();
    });

    it('shows empty message when agents list empty', () => {
      mockUseBackendConfig.mockReturnValue(
        createMockHookReturn({
          config: createMockConfig({ agents: [] }),
        })
      );

      render(<SettingsView />);

      expect(screen.queryByTestId('agent-overrides-table')).not.toBeInTheDocument();
      expect(screen.getByTestId('no-overrides-message')).toBeInTheDocument();
      expect(screen.getByText('No per-agent overrides configured.')).toBeInTheDocument();
    });
  });

  describe('className prop', () => {
    it('applies custom className', () => {
      mockUseBackendConfig.mockReturnValue(createMockHookReturn());

      render(<SettingsView className="custom-settings" />);

      const settingsView = screen.getByTestId('settings-view');
      expect(settingsView).toHaveClass('custom-settings');
    });
  });
});
