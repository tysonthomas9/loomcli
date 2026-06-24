package driver

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	PatchBackApplied         = "applied"
	PatchBackBaseUnreachable = "base_ref_unreachable"
	PatchBackBaseMismatch    = "base_ref_mismatch"
	PatchBackConflict        = "patch_conflict"
	PatchBackApplyFailed     = "patch_apply_failed"
)

type PatchBackOptions struct {
	WorktreePath string
	BaseRef      string
	Patch        []byte
}

type PatchBackResult struct {
	Status         string `json:"status"`
	Applied        bool   `json:"applied"`
	PreservePatch  bool   `json:"preservePatch"`
	BaseRef        string `json:"baseRef,omitempty"`
	BaseSHA        string `json:"baseSha,omitempty"`
	CurrentHEAD    string `json:"currentHead,omitempty"`
	ErrorClass     string `json:"errorClass,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	PreservedPatch []byte `json:"-"`
}

func ApplyPatchBack(ctx context.Context, opts PatchBackOptions) (*PatchBackResult, error) {
	opts.WorktreePath = strings.TrimSpace(opts.WorktreePath)
	opts.BaseRef = strings.TrimSpace(opts.BaseRef)
	if opts.WorktreePath == "" || opts.BaseRef == "" || len(bytes.TrimSpace(opts.Patch)) == 0 {
		return nil, fmt.Errorf("worktree path, base ref, and patch required: %w", domain.ErrInvalid)
	}
	result := &PatchBackResult{BaseRef: opts.BaseRef}
	baseSHA, baseErr := gitOutput(ctx, opts.WorktreePath, nil, "rev-parse", "--verify", opts.BaseRef+"^{commit}")
	if baseErr != nil {
		result.Status = PatchBackBaseUnreachable
		result.PreservePatch = true
		result.ErrorClass = PatchBackBaseUnreachable
		result.ErrorMessage = strings.TrimSpace(baseErr.Error())
		result.PreservedPatch = append([]byte(nil), opts.Patch...)
		return result, nil
	}
	result.BaseSHA = strings.TrimSpace(baseSHA)
	headSHA, err := gitOutput(ctx, opts.WorktreePath, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolve worktree HEAD: %w", err)
	}
	result.CurrentHEAD = strings.TrimSpace(headSHA)
	if result.CurrentHEAD != result.BaseSHA {
		result.Status = PatchBackBaseMismatch
		result.PreservePatch = true
		result.ErrorClass = PatchBackBaseMismatch
		result.ErrorMessage = fmt.Sprintf("worktree HEAD %s does not match patch base %s", result.CurrentHEAD, result.BaseSHA)
		result.PreservedPatch = append([]byte(nil), opts.Patch...)
		return result, nil
	}
	if _, err := gitOutput(ctx, opts.WorktreePath, opts.Patch, "apply", "--check"); err != nil {
		result.Status = PatchBackConflict
		result.PreservePatch = true
		result.ErrorClass = PatchBackConflict
		result.ErrorMessage = strings.TrimSpace(err.Error())
		result.PreservedPatch = append([]byte(nil), opts.Patch...)
		return result, nil
	}
	if _, err := gitOutput(ctx, opts.WorktreePath, opts.Patch, "apply"); err != nil {
		result.Status = PatchBackApplyFailed
		result.PreservePatch = true
		result.ErrorClass = PatchBackApplyFailed
		result.ErrorMessage = strings.TrimSpace(err.Error())
		result.PreservedPatch = append([]byte(nil), opts.Patch...)
		return result, nil
	}
	result.Status = PatchBackApplied
	result.Applied = true
	return result, nil
}

func gitOutput(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed executable; args are controlled by driver patch-back code.
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s: %w", msg, err)
	}
	return stdout.String(), nil
}
