package types

import (
	"context"
	"testing"
)

// mockIssueProvider is a test implementation of IssueProvider
type mockIssueProvider struct {
	issues []*Issue
	prefix string
}

func (m *mockIssueProvider) GetOpenIssues(ctx context.Context) ([]*Issue, error) {
	return m.issues, nil
}

func (m *mockIssueProvider) GetIssuePrefix() string {
	if m.prefix == "" {
		return "loom"
	}
	return m.prefix
}

func TestIssueProvider_Interface(t *testing.T) {
	t.Parallel()

	// Verify mockIssueProvider implements IssueProvider
	var _ IssueProvider = (*mockIssueProvider)(nil)

	// Test the mock implementation
	provider := &mockIssueProvider{
		issues: []*Issue{
			{ID: "loom-1", Title: "Test 1", Status: StatusOpen},
			{ID: "loom-2", Title: "Test 2", Status: StatusInProgress},
		},
		prefix: "test",
	}

	// Test GetOpenIssues
	issues, err := provider.GetOpenIssues(context.Background())
	if err != nil {
		t.Errorf("GetOpenIssues() error: %v", err)
	}
	if len(issues) != 2 {
		t.Errorf("GetOpenIssues() returned %d issues, want 2", len(issues))
	}

	// Test GetIssuePrefix
	if prefix := provider.GetIssuePrefix(); prefix != "test" {
		t.Errorf("GetIssuePrefix() = %q, want %q", prefix, "test")
	}
}

func TestIssueProvider_DefaultPrefix(t *testing.T) {
	t.Parallel()

	provider := &mockIssueProvider{}

	// Empty prefix should default to "loom"
	if prefix := provider.GetIssuePrefix(); prefix != "loom" {
		t.Errorf("GetIssuePrefix() = %q, want %q (default)", prefix, "loom")
	}
}

func TestIssueProvider_EmptyIssues(t *testing.T) {
	t.Parallel()

	provider := &mockIssueProvider{
		issues: []*Issue{},
	}

	issues, err := provider.GetOpenIssues(context.Background())
	if err != nil {
		t.Errorf("GetOpenIssues() error: %v", err)
	}
	if issues == nil {
		t.Error("GetOpenIssues() should return empty slice, not nil")
	}
	if len(issues) != 0 {
		t.Errorf("GetOpenIssues() returned %d issues, want 0", len(issues))
	}
}
