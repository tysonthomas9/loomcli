package domain

// TaskDeliveryRequirement is the minimum durable outcome a host must verify
// before accepting a task run.
type TaskDeliveryRequirement string

const (
	TaskDeliveryWorkingCopy TaskDeliveryRequirement = "working_copy"
	TaskDeliveryPullRequest TaskDeliveryRequirement = "pull_request"
)

func (r TaskDeliveryRequirement) Valid() bool {
	return r == "" || r == TaskDeliveryWorkingCopy || r == TaskDeliveryPullRequest
}
