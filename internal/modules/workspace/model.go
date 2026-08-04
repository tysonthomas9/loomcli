package workspace

import "time"

// Reference is the immutable Workspace catalog projection used by
// cross-capability coordinators.
type Reference struct {
	Key           string
	Name          string
	Description   string
	State         string
	ErrorMessage  string
	DefaultBranch string
	DesignFormat  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
