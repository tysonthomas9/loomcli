package loomgit

import "github.com/tysonthomas9/loomcli/internal/loomgit/internal/errcode"

// Error and Code form the public Loom Git error contract.
type Error = errcode.Error
type Code = errcode.Code

const (
	ReviewRequired        = errcode.ReviewRequired
	Conflict              = errcode.Conflict
	Stale                 = errcode.Stale
	CaptureIncomplete     = errcode.CaptureIncomplete
	UnsavedWork           = errcode.UnsavedWork
	Protected             = errcode.Protected
	RepoSelectionRequired = errcode.RepoSelectionRequired
	LineageUnresolved     = errcode.LineageUnresolved
	TaskCopyCreateFailed  = errcode.TaskCopyCreateFailed
	BaseRefUnresolvable   = errcode.BaseRefUnresolvable
	StaleSubject          = errcode.StaleSubject
	StackLocked           = errcode.StackLocked
	StackNotLinear        = errcode.StackNotLinear
	RefNamespaceConflict  = errcode.RefNamespaceConflict
	ProviderStackLimit    = errcode.ProviderStackLimit
	Diverged              = errcode.Diverged
	DependencyAbandoned   = errcode.DependencyAbandoned
	MergeNotAuthorized    = errcode.MergeNotAuthorized
	MergeBlocked          = errcode.MergeBlocked
	MergeQueueRequired    = errcode.MergeQueueRequired
	ModeMismatch          = errcode.ModeMismatch
	IntegrityMissing      = errcode.IntegrityMissing
	AttentionRequired     = errcode.AttentionRequired
	ApplyPending          = errcode.ApplyPending
	SwapHeld              = errcode.SwapHeld
	RestackConflict       = errcode.RestackConflict
	RevisionSuperseded    = errcode.RevisionSuperseded
	HashMismatch          = errcode.HashMismatch
	CaptureFailed         = errcode.CaptureFailed
	SecretPathRefused     = errcode.SecretPathRefused
	WorkspaceUnsupported  = errcode.WorkspaceUnsupported
)

var ErrorCodes = errcode.All

func NewError(code Code, message string, cause error) *Error {
	return errcode.New(code, message, cause)
}
