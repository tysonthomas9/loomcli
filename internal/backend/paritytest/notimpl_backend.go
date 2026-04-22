//go:build parity

package paritytest

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// notImplementedBackend is a zero-value IssueBackend whose every method
// returns backend.ErrNotImplemented. Adapters that only implement a
// subset of the IssueBackend surface embed this and override just the
// methods they actually handle — so instead of 21 notImpl() stubs living
// in the adapter file, the stub surface is exactly one type definition.
//
// BackendName intentionally stays abstract here so the embedding type
// must supply a real identifier; otherwise every adapter would diff
// against "not-implemented-backend" and muddy the report.
type notImplementedBackend struct{}

// Compile-time check: notImplementedBackend must satisfy every
// IssueBackend method except BackendName (which the embedder supplies).
// We don't assert IssueBackend here because that would require
// BackendName — embedders provide it.

func (notImplementedBackend) Get(_ context.Context, _ string) (*backend.IssueDetailData, error) {
	return nil, notImpl("Get")
}

func (notImplementedBackend) List(_ context.Context, _ backend.ListOpts) ([]backend.IssueData, error) {
	return nil, notImpl("List")
}

func (notImplementedBackend) Ready(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
	return nil, notImpl("Ready")
}

func (notImplementedBackend) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	return nil, notImpl("Blocked")
}

func (notImplementedBackend) Stats(_ context.Context) (*backend.StatsData, error) {
	return nil, notImpl("Stats")
}

func (notImplementedBackend) Count(_ context.Context, _ backend.CountOpts) (int, error) {
	return 0, notImpl("Count")
}

func (notImplementedBackend) GetChildren(_ context.Context, _ string) ([]backend.IssueData, error) {
	return nil, notImpl("GetChildren")
}

func (notImplementedBackend) SearchIssues(_ context.Context, _ string, _ int) ([]backend.IssueData, error) {
	return nil, notImpl("SearchIssues")
}

func (notImplementedBackend) Create(_ context.Context, _ backend.CreateParams) (*backend.IssueData, error) {
	return nil, notImpl("Create")
}

func (notImplementedBackend) Update(_ context.Context, _ string, _ backend.UpdateParams) error {
	return notImpl("Update")
}

func (notImplementedBackend) ClaimIssue(_ context.Context, _ string, _ time.Duration) error {
	return notImpl("ClaimIssue")
}

func (notImplementedBackend) DeferIssue(_ context.Context, _ string, _ time.Time) error {
	return notImpl("DeferIssue")
}

func (notImplementedBackend) UndeferIssue(_ context.Context, _ string) error {
	return notImpl("UndeferIssue")
}

func (notImplementedBackend) Close(_ context.Context, _ string, _ backend.CloseParams) (*backend.CloseResult, error) {
	return nil, notImpl("Close")
}

func (notImplementedBackend) Reopen(_ context.Context, _ string, _ backend.ReopenParams) error {
	return notImpl("Reopen")
}

func (notImplementedBackend) Delete(_ context.Context, _ backend.DeleteParams) error {
	return notImpl("Delete")
}

func (notImplementedBackend) AddDependency(_ context.Context, _ backend.DepAddParams) error {
	return notImpl("AddDependency")
}

func (notImplementedBackend) RemoveDependency(_ context.Context, _ backend.DepRemoveParams) error {
	return notImpl("RemoveDependency")
}

func (notImplementedBackend) AddLabel(_ context.Context, _, _ string) error {
	return notImpl("AddLabel")
}

func (notImplementedBackend) RemoveLabel(_ context.Context, _, _ string) error {
	return notImpl("RemoveLabel")
}

func (notImplementedBackend) ListComments(_ context.Context, _ string) ([]backend.CommentData, error) {
	return nil, notImpl("ListComments")
}

func (notImplementedBackend) AddComment(_ context.Context, _ backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, notImpl("AddComment")
}

func (notImplementedBackend) ListEvents(_ context.Context, _ string, _ int) ([]backend.EventData, error) {
	return nil, notImpl("ListEvents")
}

func (notImplementedBackend) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, notImpl("Batch")
}

func (notImplementedBackend) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, notImpl("GetMutations")
}

func (notImplementedBackend) WaitForMutations(_ context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
	return nil, notImpl("WaitForMutations")
}

// notImpl is shared between this file and fleetadapter.go — any paritytest
// adapter that embeds notImplementedBackend gets consistent error shape
// for every unimplemented method.
func notImpl(op string) error {
	return backend.ErrNotImplemented(op, "not implemented in paritytest fleetDBAdapter MVP")
}
