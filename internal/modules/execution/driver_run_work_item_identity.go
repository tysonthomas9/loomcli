package execution

import (
	"crypto/sha256"
	"encoding/hex"
)

const driverRunWorkItemRequestIDMaxLength = 128

// ClaimDriverRunWorkItemRequestID returns the stable, unambiguous
// claim-command identity for exactly one DriverRun and Work Item. Always
// hashing avoids delimiter collisions when either opaque ID contains a colon
// while staying below Fleet's 128-byte command-identity bound.
func ClaimDriverRunWorkItemRequestID(driverRunID, workItemID string) string {
	return driverRunWorkItemRequestID("claim-work-item", "loom-driver-run-work-item-claim", driverRunID, workItemID)
}

// ReleaseDriverRunWorkItemRequestID returns the stable release-command
// identity for exactly one DriverRun and Work Item.
func ReleaseDriverRunWorkItemRequestID(driverRunID, workItemID string) string {
	return driverRunWorkItemRequestID("release-work-item", "loom-driver-run-work-item-release", driverRunID, workItemID)
}

func driverRunWorkItemRequestID(prefix, digestDomain, driverRunID, workItemID string) string {
	digest := sha256.Sum256([]byte(digestDomain + "\x00" + driverRunID + "\x00" + workItemID))
	return prefix + ":sha256:" + hex.EncodeToString(digest[:])
}

// DriverRunWorkItemClaimActionID is the exact Fleet action-ledger identity
// associated with a claim request.
func DriverRunWorkItemClaimActionID(claimRequestID string) string {
	return "driver-run-work-item-claim:" + claimRequestID
}

// DriverRunWorkItemReleaseActionID is the exact Fleet action-ledger identity
// associated with a release request.
func DriverRunWorkItemReleaseActionID(releaseRequestID string) string {
	return "driver-run-work-item-release:" + releaseRequestID
}
