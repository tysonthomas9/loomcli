package gen

// Compatibility aliases preserve the public names emitted before adding the
// AgentHistorySession enums. oapi-codegen avoids package-level enum collisions
// by renaming otherwise unrelated constants; callers should not have to change
// merely because the OpenAPI document gained another enum.
const (
	Dead     = TreeNodeAgentStateDead
	Done     = TreeNodeAgentStateDone
	Idle     = TreeNodeAgentStateIdle
	Running  = TreeNodeAgentStateRunning
	Spawning = TreeNodeAgentStateSpawning
	Stopped  = TreeNodeAgentStateStopped
	Stuck    = TreeNodeAgentStateStuck
	Working  = TreeNodeAgentStateWorking

	ListReadyParamsTypeBug     = Bug
	ListReadyParamsTypeChore   = Chore
	ListReadyParamsTypeEpic    = Epic
	ListReadyParamsTypeFeature = Feature
	ListReadyParamsTypeTask    = Task
)
