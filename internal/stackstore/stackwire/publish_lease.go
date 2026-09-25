package stackwire

import "time"

// PublishLease mirrors fleet-db's StackPublishLease JSON shape.
//
// The FleetDB token fences lease generations only; it does not fence GitHub.
type PublishLease struct {
	Token      string    `json:"token"`
	StackID    string    `json:"stack_id"`
	Holder     string    `json:"holder"`
	Generation int64     `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
	ReuseAfter time.Time `json:"reuse_after"`
}

// AcquirePublishLease is the body of POST .../publish-lease.
type AcquirePublishLease struct {
	Holder       string `json:"holder"`
	TTLSeconds   *int   `json:"ttl_seconds,omitempty"`
	GraceSeconds *int   `json:"grace_seconds,omitempty"`
}

// RenewPublishLease is the body of PUT .../publish-lease.
type RenewPublishLease struct {
	Token        string `json:"token"`
	TTLSeconds   *int   `json:"ttl_seconds,omitempty"`
	GraceSeconds *int   `json:"grace_seconds,omitempty"`
}
