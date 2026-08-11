package automation

import (
	"context"
	"fmt"
	"strings"
)

func (s *Service) GetBinding(ctx context.Context, workspace, bindingID string) (*Binding, error) {
	workspace, err := normalizeRequired("workspace", workspace)
	if err != nil {
		return nil, err
	}
	bindingID, err = requireCanonical("binding id", bindingID)
	if err != nil {
		return nil, err
	}
	return s.loadBinding(ctx, workspace, bindingID)
}

func (s *Service) loadBinding(ctx context.Context, workspace, bindingID string) (*Binding, error) {
	if s == nil || s.bindings == nil {
		return nil, ErrUnavailable
	}
	binding, err := s.bindings.GetBinding(ctx, workspace, bindingID)
	if err != nil {
		return nil, fmt.Errorf("get binding %q: %w", bindingID, err)
	}
	if err := validatePersistedBinding(binding, workspace, bindingID); err != nil {
		return nil, err
	}
	return cloneBinding(binding), nil
}

func (s *Service) ListBindings(ctx context.Context, workspace string, filter BindingFilter) ([]*Binding, error) {
	workspace, err := normalizeRequired("workspace", workspace)
	if err != nil {
		return nil, err
	}
	if filter.Limit < 0 {
		return nil, fmt.Errorf("limit cannot be negative: %w", ErrInvalid)
	}
	if s == nil || s.bindings == nil {
		return nil, ErrUnavailable
	}
	filter.SourceKind = strings.TrimSpace(filter.SourceKind)
	filter.RouteKey = strings.TrimSpace(filter.RouteKey)
	filter.DriverID = strings.TrimSpace(filter.DriverID)
	filter.TargetAgentServiceID = strings.TrimSpace(filter.TargetAgentServiceID)
	bindings, err := s.bindings.ListBindings(ctx, workspace, filter)
	if err != nil {
		return nil, fmt.Errorf("list bindings: %w", err)
	}
	out := make([]*Binding, 0, len(bindings))
	for _, binding := range bindings {
		if err := validatePersistedBinding(binding, workspace, ""); err != nil {
			return nil, err
		}
		out = append(out, cloneBinding(binding))
	}
	return out, nil
}

func (s *Service) GetEvent(ctx context.Context, workspace, eventID string) (*Event, error) {
	workspace, err := normalizeRequired("workspace", workspace)
	if err != nil {
		return nil, err
	}
	eventID, err = requireCanonical("event id", eventID)
	if err != nil {
		return nil, err
	}
	return s.loadEvent(ctx, workspace, eventID)
}

func (s *Service) loadEvent(ctx context.Context, workspace, eventID string) (*Event, error) {
	if s == nil || s.events == nil {
		return nil, ErrUnavailable
	}
	event, err := s.events.GetEvent(ctx, workspace, eventID)
	if err != nil {
		return nil, fmt.Errorf("get event %q: %w", eventID, err)
	}
	event = cloneEvent(event)
	event.NormalizeProvenance()
	if err := validatePersistedEvent(event, workspace, eventID); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *Service) ListEvents(ctx context.Context, workspace string, filter EventFilter) ([]*Event, error) {
	workspace, err := normalizeRequired("workspace", workspace)
	if err != nil {
		return nil, err
	}
	if filter.Limit < 0 {
		return nil, fmt.Errorf("limit cannot be negative: %w", ErrInvalid)
	}
	if s == nil || s.events == nil {
		return nil, ErrUnavailable
	}
	filter.BindingID = strings.TrimSpace(filter.BindingID)
	filter.SourceKind = strings.TrimSpace(filter.SourceKind)
	events, err := s.events.ListEvents(ctx, workspace, filter)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	out := make([]*Event, 0, len(events))
	for _, event := range events {
		event = cloneEvent(event)
		event.NormalizeProvenance()
		if err := validatePersistedEvent(event, workspace, ""); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, nil
}

func (s *Service) GetDelivery(ctx context.Context, workspace, deliveryID string) (*Delivery, error) {
	workspace, err := normalizeRequired("workspace", workspace)
	if err != nil {
		return nil, err
	}
	deliveryID, err = requireCanonical("delivery id", deliveryID)
	if err != nil {
		return nil, err
	}
	if s == nil || s.deliveries == nil {
		return nil, ErrUnavailable
	}
	delivery, err := s.deliveries.GetDelivery(ctx, workspace, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("get delivery %q: %w", deliveryID, err)
	}
	if err := validatePersistedDelivery(delivery, workspace, deliveryID, ""); err != nil {
		return nil, err
	}
	return cloneDelivery(delivery), nil
}

func (s *Service) ListDeliveries(ctx context.Context, workspace string, filter DeliveryFilter) ([]*Delivery, error) {
	workspace, err := normalizeRequired("workspace", workspace)
	if err != nil {
		return nil, err
	}
	if filter.Limit < 0 {
		return nil, fmt.Errorf("limit cannot be negative: %w", ErrInvalid)
	}
	if filter.Status != "" && !filter.Status.IsValid() {
		return nil, fmt.Errorf("unsupported delivery status %q: %w", filter.Status, ErrInvalid)
	}
	if s == nil || s.deliveries == nil {
		return nil, ErrUnavailable
	}
	filter.EventID = strings.TrimSpace(filter.EventID)
	filter.BindingID = strings.TrimSpace(filter.BindingID)
	deliveries, err := s.deliveries.ListDeliveries(ctx, workspace, filter)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	out := make([]*Delivery, 0, len(deliveries))
	for _, delivery := range deliveries {
		if err := validatePersistedDelivery(delivery, workspace, "", filter.EventID); err != nil {
			return nil, err
		}
		out = append(out, cloneDelivery(delivery))
	}
	return out, nil
}

func validatePersistedBinding(binding *Binding, workspace, bindingID string) error {
	if binding == nil || strings.TrimSpace(binding.BindingID) == "" || binding.BindingID != strings.TrimSpace(binding.BindingID) {
		return ErrInvalidPersistedState
	}
	if err := validateWorkspace(binding.WorkspaceKey, workspace); err != nil {
		return err
	}
	if bindingID != "" && binding.BindingID != bindingID {
		return ErrInvalidPersistedState
	}
	return nil
}

func validatePersistedEvent(event *Event, workspace, eventID string) error {
	if event == nil || strings.TrimSpace(event.EventID) == "" || event.EventID != strings.TrimSpace(event.EventID) {
		return ErrInvalidPersistedState
	}
	if err := validateWorkspace(event.WorkspaceKey, workspace); err != nil {
		return err
	}
	if eventID != "" && event.EventID != eventID {
		return ErrInvalidPersistedState
	}
	if event.HopDepth < 0 || !validEventOrigin(event.Origin) || strings.TrimSpace(event.SourceKind) == "" ||
		strings.TrimSpace(event.SourceEventID) == "" || strings.TrimSpace(event.EventType) == "" ||
		strings.TrimSpace(event.RouteKey) == "" || strings.TrimSpace(event.IdempotencyKey) == "" ||
		event.OccurredAt.IsZero() || event.ReceivedAt.IsZero() {
		return ErrInvalidPersistedState
	}
	if event.Origin == EventOriginWorkflow && strings.TrimSpace(event.EmittingRunID) == "" {
		return ErrInvalidPersistedState
	}
	return nil
}

func validEventOrigin(origin EventOrigin) bool {
	switch origin {
	case EventOriginExternal, EventOriginWorkflow, EventOriginSystem:
		return true
	default:
		return false
	}
}

func validatePersistedDelivery(delivery *Delivery, workspace, deliveryID, eventID string) error {
	if delivery == nil || strings.TrimSpace(delivery.DeliveryID) == "" || delivery.DeliveryID != strings.TrimSpace(delivery.DeliveryID) {
		return ErrInvalidPersistedState
	}
	if err := validateWorkspace(delivery.WorkspaceKey, workspace); err != nil {
		return err
	}
	if deliveryID != "" && delivery.DeliveryID != deliveryID {
		return ErrInvalidPersistedState
	}
	if eventID != "" && delivery.TriggerEventID != eventID {
		return ErrInvalidPersistedState
	}
	if strings.TrimSpace(delivery.TriggerEventID) == "" || strings.TrimSpace(delivery.TriggerBindingID) == "" || !delivery.Status.IsValid() {
		return ErrInvalidPersistedState
	}
	if delivery.Attempt < 1 || delivery.CreatedAt.IsZero() || delivery.UpdatedAt.IsZero() {
		return ErrInvalidPersistedState
	}
	return nil
}
