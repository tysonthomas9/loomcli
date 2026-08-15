// Package agentprovision owns create-or-repair lifecycle choreography for
// scripted-role instances and other durable records that share that shape.
package agentprovision

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Reconciler supplies entity-specific persistence and diff behavior. Ensure
// owns only the create/get race, tombstone, and patch ordering choreography.
type Reconciler[T, C, P any] struct {
	Get         func(context.Context) (*T, error)
	Create      func(context.Context, C) (*T, error)
	Archived    func(*T) bool
	Diff        func(*T) (P, bool)
	BeforePatch func(context.Context, *T, P) error
	Patch       func(context.Context, *T, P) (*T, error)
}

// Ensure creates first, then reconciles the existing record on
// ErrAlreadyExists. A create/get delete race gets one bounded create retry and
// a final re-get if that retry loses to another creator.
func Ensure[T, C, P any](ctx context.Context, create C, r Reconciler[T, C, P]) (*T, error) {
	entity, err := r.Create(ctx, create)
	if err == nil {
		return entity, nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return nil, err
	}

	entity, err = r.Get(ctx)
	if errors.Is(err, domain.ErrNotFound) {
		entity, err = r.Create(ctx, create)
		if errors.Is(err, domain.ErrAlreadyExists) {
			entity, err = r.Get(ctx)
		}
	}
	if err != nil {
		return nil, err
	}
	if r.Archived != nil && r.Archived(entity) {
		return nil, domain.ErrInvalidTransition
	}
	if r.Diff == nil {
		return entity, nil
	}
	patch, changed := r.Diff(entity)
	if !changed {
		return entity, nil
	}
	if r.BeforePatch != nil {
		if err := r.BeforePatch(ctx, entity, patch); err != nil {
			return nil, err
		}
	}
	return r.Patch(ctx, entity, patch)
}
