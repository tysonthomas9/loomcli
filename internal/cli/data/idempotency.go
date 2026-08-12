package data

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// computeCreateKey derives the default X-Idempotency-Key for a create:
// sha256 over a UTC date bucket and the server-visible create body.
//
// The hash input is the fleet-db body projection (CreateParams.FleetCreateBody),
// NOT the full CreateParams: fields fleet-db drops (id, acceptance_criteria,
// created_by, estimated_minutes, dependencies) must not
// differentiate keys, or two requests that persist identically would mint
// duplicates. Using the same projection the wire request is built from also
// keeps the key aligned byte-for-byte with the body fleet-db fingerprints,
// so a default key can never trip the key-reuse-with-different-body 409.
//
// The date bucket bounds false dedup: identical creates on different days
// never collapse. Workspace and actor scoping happen server-side, where the
// authenticated actor is actually known.
func computeCreateKey(params workitems.CreateCommand) (string, error) {
	return params.DefaultIdempotencyKey(time.Now())
}

// isAlreadyClosedConflict reports whether err is the "issue is already
// closed" conflict — the one close error that means the desired state is
// already true. Other KindConflict closes (open blockers, dependencies)
// must keep failing.
func isAlreadyClosedConflict(err error) bool {
	return workitems.IsAlreadyClosedConflict(err)
}
