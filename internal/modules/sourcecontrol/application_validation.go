package sourcecontrol

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// ValidateRepositoryAdmissionCheckoutCommand validates the complete
// Workspace-owned admission coordinate before composition issues Source
// Control authority.
func ValidateRepositoryAdmissionCheckoutCommand(
	command RepositoryAdmissionCheckoutCommand,
) error {
	if _, err := requireCanonical("workspace", command.WorkspaceKey); err != nil {
		return err
	}
	if !validOpaqueAdmissionID(command.AdmissionID) {
		return fmt.Errorf("%w: admission id must be 32 lowercase hex characters", ErrInvalid)
	}
	if _, err := requireCanonical("repository ref", command.RepositoryRef); err != nil {
		return err
	}
	if command.OwnerID == "" ||
		command.OwnerID != strings.TrimSpace(command.OwnerID) ||
		len(command.OwnerID) > 200 ||
		strings.IndexFunc(command.OwnerID, func(char rune) bool {
			return char < 0x20 || char == 0x7f
		}) >= 0 {
		return fmt.Errorf("%w: repository admission owner is invalid", ErrInvalid)
	}
	if !validOpaqueAdmissionID(command.OwnerGenerationID) {
		return fmt.Errorf(
			"%w: repository admission owner generation must be 32 lowercase hex characters",
			ErrInvalid,
		)
	}
	const fingerprintPrefix = "sha256:"
	if !strings.HasPrefix(command.SpecFingerprint, fingerprintPrefix) ||
		!validSHA256Hex(strings.TrimPrefix(
			command.SpecFingerprint,
			fingerprintPrefix,
		)) {
		return fmt.Errorf(
			"%w: repository admission spec fingerprint must be canonical sha256",
			ErrInvalid,
		)
	}
	return nil
}

// ValidateTaskCheckoutCommand validates the complete authority-free task
// materialization intent before composition performs clone/fetch side effects.
func ValidateTaskCheckoutCommand(command TaskCheckoutCommand) error {
	if _, err := requireCanonical("workspace", command.WorkspaceKey); err != nil {
		return err
	}
	if _, err := requireCanonical("task run id", command.TaskRunID); err != nil {
		return err
	}
	if _, err := requireCanonical("repository ref", command.RepositoryRef); err != nil {
		return err
	}
	if _, err := requireFetchSourceRef("refs/heads/" + command.BaseBranch); err != nil {
		return err
	}
	return nil
}

// ValidatePullRequestCheckoutCommand validates the immutable PR subject and
// both bounded remote-read coordinates before any repository is materialized.
func ValidatePullRequestCheckoutCommand(command PullRequestCheckoutCommand) error {
	if _, err := requireCanonical("workspace", command.WorkspaceKey); err != nil {
		return err
	}
	if _, err := requireCanonical("review id", command.ReviewID); err != nil {
		return err
	}
	if _, err := requireCanonical("repository ref", command.RepositoryRef); err != nil {
		return err
	}
	if command.Number <= 0 {
		return fmt.Errorf("%w: pull request number must be positive", ErrInvalid)
	}
	if _, err := normalizeCommitSHA(command.HeadCommit); err != nil {
		return err
	}
	if _, err := requireFetchSourceRef("refs/pull/" + fmt.Sprintf("%d", command.Number) + "/head"); err != nil {
		return err
	}
	if _, err := requireFetchSourceRef("refs/heads/" + command.BaseBranch); err != nil {
		return err
	}
	return nil
}

func validOpaqueAdmissionID(value string) bool {
	if len(value) != 32 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}
