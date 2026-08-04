package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Service struct {
	store CatalogStore
}

var _ API = (*Service)(nil)

func New(store CatalogStore) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("compose Workspace: catalog store is required: %w", ErrUnavailable)
	}
	return &Service{store: store}, nil
}

func (s *Service) Resolve(ctx context.Context, query ResolveQuery) (*Reference, error) {
	reference := strings.TrimSpace(query.Reference)
	if reference == "" {
		return nil, fmt.Errorf("workspace reference is required: %w", ErrInvalid)
	}
	value, err := s.store.GetByKey(ctx, reference)
	if errors.Is(err, ErrNotFound) {
		value, err = s.store.GetByName(ctx, reference)
	}
	if err != nil {
		return nil, err
	}
	if value == nil || strings.TrimSpace(value.Key) == "" || strings.TrimSpace(value.Name) == "" {
		return nil, fmt.Errorf("resolve workspace %q returned an invalid reference: %w", reference, ErrInvalidPersistedState)
	}
	copy := *value
	return &copy, nil
}
