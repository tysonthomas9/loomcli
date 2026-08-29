package domain

import (
	"fmt"
	"time"
)

// SkillMaterializationLease is fleet-db's client-facing live lease.
type SkillMaterializationLease struct {
	Token     string    `json:"token"`
	TargetKey string    `json:"target_key"`
	Holder    string    `json:"holder"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SkillMaterializationLeaseConflictError carries the current holder metadata
// returned by fleet-db on acquire conflicts.
type SkillMaterializationLeaseConflictError struct {
	Message   string
	Holder    string
	ExpiresAt time.Time
}

func (e *SkillMaterializationLeaseConflictError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("skill materialization target is leased by %s until %s", e.Holder, e.ExpiresAt.Format(time.RFC3339Nano))
}

func (e *SkillMaterializationLeaseConflictError) Unwrap() error {
	return ErrSkillMaterializationLeaseConflict
}
