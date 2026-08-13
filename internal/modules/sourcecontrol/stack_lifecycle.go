package sourcecontrol

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// StackLifecycleService owns stack input validation, default append behavior,
// idempotent topology reconciliation, and lineage projection.
type StackLifecycleService struct {
	store StackLifecycleStore
	now   func() time.Time
}

func NewStackLifecycle(store StackLifecycleStore, now func() time.Time) (*StackLifecycleService, error) {
	if store == nil || now == nil {
		return nil, fmt.Errorf("compose Source Control stack lifecycle: store and clock are required: %w", ErrUnavailable)
	}
	return &StackLifecycleService{store: store, now: now}, nil
}

func (service *StackLifecycleService) EnsureStack(ctx context.Context, command EnsureStackCommand) (*Stack, error) {
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	if err := validateStackCoordinates(command.WorkspaceKey, command.StackID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.Repository) == "" || strings.TrimSpace(command.RootBase) == "" {
		return nil, fmt.Errorf("stack repository and root base are required: %w", ErrInvalid)
	}
	stack := Stack{
		ID: command.StackID, WorkspaceKey: command.WorkspaceKey, Repository: command.Repository,
		RootBase: command.RootBase, DefaultCommitMode: command.DefaultCommitMode,
	}
	if err := service.store.EnsureStackRecord(ctx, stack); err != nil {
		return nil, err
	}
	return service.GetStack(ctx, command.WorkspaceKey, command.StackID)
}

func (service *StackLifecycleService) ListStacks(ctx context.Context, workspace string) ([]Stack, error) {
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("stack workspace is required: %w", ErrInvalid)
	}
	return service.store.ListStackRecords(ctx, workspace)
}

func (service *StackLifecycleService) GetStack(ctx context.Context, workspace, stackID string) (*Stack, error) {
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	if err := validateStackCoordinates(workspace, stackID); err != nil {
		return nil, err
	}
	return service.store.GetStackRecord(ctx, workspace, stackID)
}

func (service *StackLifecycleService) ListStackNodes(ctx context.Context, workspace, stackID string) ([]StackNode, error) {
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	if err := validateStackCoordinates(workspace, stackID); err != nil {
		return nil, err
	}
	return service.store.ListStackNodeRecords(ctx, workspace, stackID)
}

func (service *StackLifecycleService) ValidateStack(ctx context.Context, workspace, stackID string) error {
	nodes, err := service.ListStackNodes(ctx, workspace, stackID)
	if err != nil {
		return err
	}
	_, err = Ordered(nodes)
	return classifyLineageValidation(err)
}

func (service *StackLifecycleService) AddStackNode(ctx context.Context, command AddStackNodeCommand) (*StackNode, error) {
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	if err := validateStackNodeCoordinates(command.WorkspaceKey, command.StackID, command.TaskID); err != nil {
		return nil, err
	}
	if command.Root && strings.TrimSpace(command.AfterTaskID) != "" {
		return nil, fmt.Errorf("root and predecessor are mutually exclusive: %w", ErrInvalid)
	}
	base := command.AfterTaskID
	if !command.Root && strings.TrimSpace(base) == "" {
		nodes, err := service.ListStackNodes(ctx, command.WorkspaceKey, command.StackID)
		if err != nil {
			return nil, err
		}
		ordered, err := Ordered(nodes)
		if err != nil {
			return nil, classifyLineageValidation(err)
		}
		if len(ordered) > 0 {
			base = ordered[len(ordered)-1].TaskID
		}
	}
	node, err := service.store.AddStackNodeRecord(
		ctx, command.WorkspaceKey, command.StackID, command.TaskID, base, command.CommitMode,
	)
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func (service *StackLifecycleService) MoveStackNode(ctx context.Context, command MoveStackNodeCommand) error {
	if service == nil || service.store == nil {
		return ErrUnavailable
	}
	if err := validateStackNodeCoordinates(command.WorkspaceKey, command.StackID, command.TaskID); err != nil {
		return err
	}
	if strings.TrimSpace(command.AfterTaskID) == "" {
		return fmt.Errorf("stack predecessor is required: %w", ErrInvalid)
	}
	return service.store.MoveStackNodeRecord(ctx, command.WorkspaceKey, command.StackID, command.TaskID, command.AfterTaskID)
}

func (service *StackLifecycleService) SetStackNodeBase(ctx context.Context, command SetStackNodeBaseCommand) error {
	if service == nil || service.store == nil {
		return ErrUnavailable
	}
	if err := validateStackNodeCoordinates(command.WorkspaceKey, command.StackID, command.TaskID); err != nil {
		return err
	}
	return service.store.SetStackNodeBaseRecord(ctx, command.WorkspaceKey, command.StackID, command.TaskID, command.BaseTaskID)
}

func (service *StackLifecycleService) RemoveStackNode(ctx context.Context, command RemoveStackNodeCommand) error {
	if service == nil || service.store == nil {
		return ErrUnavailable
	}
	if err := validateStackNodeCoordinates(command.WorkspaceKey, command.StackID, command.TaskID); err != nil {
		return err
	}
	return service.store.RemoveStackNodeRecord(ctx, command.WorkspaceKey, command.StackID, command.TaskID)
}

func (service *StackLifecycleService) RecordStackNodePublication(
	ctx context.Context,
	command RecordStackNodePublicationCommand,
) error {
	if service == nil || service.store == nil || service.now == nil {
		return ErrUnavailable
	}
	if err := validateStackNodeCoordinates(command.WorkspaceKey, command.StackID, command.TaskID); err != nil {
		return err
	}
	mutation := StackNodePublicationMutation{
		State: command.State, PRNumber: command.PRNumber, PRURL: command.PRURL, OutputSHA: command.OutputSHA,
	}
	switch command.State {
	case StackPublicationPublished:
		publishedAt := service.now().UTC()
		mutation.PublishedAt = &publishedAt
	case StackPublicationMerged, StackPublicationEmpty:
		if command.PRNumber != 0 || strings.TrimSpace(command.PRURL) != "" || strings.TrimSpace(command.OutputSHA) != "" {
			return fmt.Errorf("terminal stack publication carries published-only fields: %w", ErrInvalid)
		}
	default:
		return fmt.Errorf("unsupported stack publication state %q: %w", command.State, ErrInvalid)
	}
	return service.store.UpdateStackNodePublicationRecord(
		ctx, command.WorkspaceKey, command.StackID, command.TaskID, mutation,
	)
}

//nolint:funlen // Binding resolution validates the complete stack projection before selecting one node.
func (service *StackLifecycleService) ResolveTaskStackBinding(
	ctx context.Context,
	workspace,
	repository,
	taskID string,
) (TaskStackBinding, bool, error) {
	if service == nil || service.store == nil {
		return TaskStackBinding{}, false, ErrUnavailable
	}
	workspace = strings.TrimSpace(workspace)
	repository = strings.TrimSpace(repository)
	taskID = strings.TrimSpace(taskID)
	if workspace == "" || repository == "" || taskID == "" {
		return TaskStackBinding{}, false, nil
	}
	stacks, err := service.ListStacks(ctx, workspace)
	if err != nil {
		return TaskStackBinding{}, false, err
	}
	var foundStack Stack
	var foundNode StackNode
	var foundByTask map[string]StackNode
	found := false
	for _, stack := range stacks {
		if stack.WorkspaceKey != workspace {
			return TaskStackBinding{}, false, fmt.Errorf("stack %q escaped workspace %q: %w", stack.ID, workspace, ErrInvalidMaterialization)
		}
		if strings.TrimSpace(stack.Repository) == "" || stack.Repository != repository {
			continue
		}
		nodes, err := service.ListStackNodes(ctx, workspace, stack.ID)
		if err != nil {
			return TaskStackBinding{}, false, err
		}
		byTask := make(map[string]StackNode, len(nodes))
		for _, node := range nodes {
			byTask[node.TaskID] = node
		}
		node, ok := byTask[taskID]
		if !ok {
			continue
		}
		if found {
			return TaskStackBinding{}, false, nil
		}
		foundStack, foundNode, foundByTask = stack, node, byTask
		found = true
	}
	if !found {
		return TaskStackBinding{}, false, nil
	}
	base, err := BaseBranchSliding(foundStack, foundNode, foundByTask)
	if err != nil {
		return TaskStackBinding{}, false, classifyLineageValidation(err)
	}
	return TaskStackBinding{
		StackID: foundStack.ID, BaseRef: base, OutputBranch: foundNode.OutputBranch,
	}, true, nil
}

//nolint:funlen,gocognit,cyclop // Reconciliation explicitly validates every lineage and conflict branch before mutation.
func (service *StackLifecycleService) ReconcileStack(ctx context.Context, command ReconcileStackCommand) (*ReconcileStackResult, error) {
	if service == nil || service.store == nil {
		return nil, ErrUnavailable
	}
	if err := validateStackCoordinates(command.Stack.WorkspaceKey, command.Stack.StackID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.Stack.Repository) == "" || strings.TrimSpace(command.Stack.RootBase) == "" {
		return nil, fmt.Errorf("stack repository and root base are required: %w", ErrInvalid)
	}
	desired := make(map[string]DesiredStackNode, len(command.Nodes))
	for _, node := range command.Nodes {
		if strings.TrimSpace(node.TaskID) == "" {
			return nil, fmt.Errorf("desired stack task is required: %w", ErrInvalid)
		}
		if _, duplicate := desired[node.TaskID]; duplicate {
			return nil, fmt.Errorf("desired stack task %q is duplicated: %w", node.TaskID, ErrInvalid)
		}
		desired[node.TaskID] = node
	}
	for _, node := range command.Nodes {
		if node.BaseTaskID != "" {
			if _, ok := desired[node.BaseTaskID]; !ok {
				return nil, fmt.Errorf("desired stack task %q has missing predecessor %q: %w", node.TaskID, node.BaseTaskID, ErrInvalid)
			}
		}
	}
	topology := make([]StackNode, len(command.Nodes))
	for index, node := range command.Nodes {
		topology[index] = StackNode{TaskID: node.TaskID, BaseTaskID: node.BaseTaskID}
	}
	if _, err := Ordered(topology); err != nil {
		return nil, fmt.Errorf("validate desired stack topology: %w", classifyLineageValidation(err))
	}
	if _, err := service.EnsureStack(ctx, command.Stack); err != nil {
		return nil, err
	}
	existingNodes, err := service.ListStackNodes(ctx, command.Stack.WorkspaceKey, command.Stack.StackID)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]StackNode, len(existingNodes))
	inserted := make(map[string]struct{}, len(existingNodes))
	for _, node := range existingNodes {
		existing[node.TaskID] = node
		inserted[node.TaskID] = struct{}{}
	}
	result := &ReconcileStackResult{}
	remaining := append([]DesiredStackNode(nil), command.Nodes...)
	for len(remaining) > 0 {
		progress := false
		next := make([]DesiredStackNode, 0, len(remaining))
		for _, node := range remaining {
			_, baseReady := inserted[node.BaseTaskID]
			if node.BaseTaskID != "" && !baseReady {
				next = append(next, node)
				continue
			}
			if current, ok := existing[node.TaskID]; ok {
				if current.BaseTaskID != node.BaseTaskID {
					if err := service.SetStackNodeBase(ctx, SetStackNodeBaseCommand{
						WorkspaceKey: command.Stack.WorkspaceKey, StackID: command.Stack.StackID,
						TaskID: node.TaskID, BaseTaskID: node.BaseTaskID,
					}); err != nil {
						return nil, err
					}
					result.Reparented = append(result.Reparented, node.TaskID)
				}
			} else {
				created, err := service.store.AddStackNodeRecord(
					ctx, command.Stack.WorkspaceKey, command.Stack.StackID, node.TaskID, node.BaseTaskID, "",
				)
				if err != nil {
					return nil, err
				}
				existing[node.TaskID] = created
				result.Created = append(result.Created, node.TaskID)
			}
			inserted[node.TaskID] = struct{}{}
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("desired stack topology cannot be ordered: %w", ErrInvalid)
		}
		remaining = next
	}
	sort.Strings(result.Created)
	sort.Strings(result.Reparented)
	stack, err := service.GetStack(ctx, command.Stack.WorkspaceKey, command.Stack.StackID)
	if err != nil {
		return nil, err
	}
	nodes, err := service.ListStackNodes(ctx, command.Stack.WorkspaceKey, command.Stack.StackID)
	if err != nil {
		return nil, err
	}
	result.Stack = *stack
	result.Nodes = nodes
	result.Lineage, err = projectStackLineage(*stack, nodes)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validateStackCoordinates(workspace, stackID string) error {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(stackID) == "" {
		return fmt.Errorf("stack workspace and id are required: %w", ErrInvalid)
	}
	return nil
}

func validateStackNodeCoordinates(workspace, stackID, taskID string) error {
	if err := validateStackCoordinates(workspace, stackID); err != nil {
		return err
	}
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("stack task is required: %w", ErrInvalid)
	}
	return nil
}

func projectStackLineage(stack Stack, nodes []StackNode) (map[string]StackLineage, error) {
	byTask := make(map[string]StackNode, len(nodes))
	for _, node := range nodes {
		byTask[node.TaskID] = node
	}
	result := make(map[string]StackLineage, len(nodes))
	for _, node := range nodes {
		base := stack.RootBase
		if node.BaseTaskID != "" {
			predecessor, ok := byTask[node.BaseTaskID]
			if !ok || strings.TrimSpace(predecessor.OutputBranch) == "" {
				return nil, fmt.Errorf("stack task %q has unresolved predecessor %q: %w", node.TaskID, node.BaseTaskID, ErrInvalidMaterialization)
			}
			base = predecessor.OutputBranch
		}
		result[node.TaskID] = StackLineage{StackID: stack.ID, BaseRef: base, OutputBranch: node.OutputBranch}
	}
	return result, nil
}

func classifyLineageValidation(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("invalid stack lineage (%v): %w", err, ErrInvalidMaterialization)
}
