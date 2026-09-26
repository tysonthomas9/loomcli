package service

import "context"

// CreateIssueMetadata contains transport-level create signals that are not
// part of the issue resource itself.
type CreateIssueMetadata struct {
	Replayed bool
	Warning  string
}

type createIssueMetadataKey struct{}

// WithCreateIssueMetadata installs a mutable response collector in ctx.
func WithCreateIssueMetadata(ctx context.Context) context.Context {
	return context.WithValue(ctx, createIssueMetadataKey{}, &CreateIssueMetadata{})
}

// SetCreateIssueMetadata records metadata for the handler that owns ctx.
func SetCreateIssueMetadata(ctx context.Context, metadata CreateIssueMetadata) {
	if collector, ok := ctx.Value(createIssueMetadataKey{}).(*CreateIssueMetadata); ok {
		*collector = metadata
	}
}

// GetCreateIssueMetadata returns the create response metadata, when collected.
func GetCreateIssueMetadata(ctx context.Context) CreateIssueMetadata {
	if collector, ok := ctx.Value(createIssueMetadataKey{}).(*CreateIssueMetadata); ok {
		return *collector
	}
	return CreateIssueMetadata{}
}
