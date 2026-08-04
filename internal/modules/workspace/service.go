package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const maxWorkspaceNameLength = 64

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
	return validatedReference(value, "resolve workspace "+reference)
}

func (s *Service) List(ctx context.Context, _ ListQuery) ([]Reference, error) {
	values, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Reference, len(values))
	for index := range values {
		value, err := validatedReference(&values[index], "list workspaces")
		if err != nil {
			return nil, err
		}
		out[index] = *value
	}
	return out, nil
}

func (s *Service) Rename(ctx context.Context, command RenameCommand) (*Reference, error) {
	name := strings.TrimSpace(command.Name)
	if err := validateName(name); err != nil {
		return nil, err
	}
	current, err := s.Resolve(ctx, ResolveQuery{Reference: command.Reference})
	if err != nil {
		return nil, err
	}
	if current.Name == name {
		return current, nil
	}
	existing, lookupErr := s.store.GetByName(ctx, name)
	switch {
	case lookupErr == nil && existing != nil && existing.Key != current.Key:
		return nil, fmt.Errorf("workspace name %q already exists: %w", name, ErrConflict)
	case lookupErr != nil && !errors.Is(lookupErr, ErrNotFound):
		return nil, lookupErr
	}
	updated, err := s.store.Rename(ctx, current.Key, name)
	if err != nil {
		return nil, err
	}
	return validatedReference(updated, "rename workspace "+current.Key)
}

func (s *Service) SetDesignFormat(ctx context.Context, command SetDesignFormatCommand) (*Reference, error) {
	format := strings.TrimSpace(command.Format)
	if format != DesignFormatMarkdown && format != DesignFormatHTML {
		return nil, fmt.Errorf("design format must be markdown or html: %w", ErrInvalid)
	}
	current, err := s.Resolve(ctx, ResolveQuery{Reference: command.Reference})
	if err != nil {
		return nil, err
	}
	if current.DesignFormat == format {
		return current, nil
	}
	updated, err := s.store.SetDesignFormat(ctx, current.Key, format)
	if err != nil {
		return nil, err
	}
	return validatedReference(updated, "set workspace design format "+current.Key)
}

func validatedReference(value *Reference, operation string) (*Reference, error) {
	if value == nil || strings.TrimSpace(value.Key) == "" || strings.TrimSpace(value.Name) == "" {
		return nil, fmt.Errorf("%s returned an invalid reference: %w", operation, ErrInvalidPersistedState)
	}
	copy := *value
	return &copy, nil
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("workspace name is required: %w", ErrInvalid)
	}
	if len(name) > maxWorkspaceNameLength {
		return fmt.Errorf("workspace name is too long (max %d characters): %w", maxWorkspaceNameLength, ErrInvalid)
	}
	for _, value := range name {
		if !((value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') || value == '-' || value == '_') {
			return fmt.Errorf("workspace name must contain only alphanumeric characters, hyphens, and underscores: %w", ErrInvalid)
		}
	}
	return nil
}
