package execution

import (
	"context"
	"time"
)

// TerminalDriverStepRepair is the narrow, system-only persistence intent used
// by Execution convergence after the parent DriverRun may already be terminal.
// Implementations must verify the exact DriverRun/DriverStep/TaskRun linkage,
// allow only monotonic nonterminal-to-terminal movement, and replay only an
// identical terminal projection under RequestID.
type TerminalDriverStepRepair struct {
	RequestID    string
	WorkspaceKey string
	DriverRunID  string
	DriverStepID string
	TaskRunID    string
	Status       DriverStepStatus
	OutputRef    string
	RepairedAt   time.Time
}

// TerminalDriverStepRepairStore is deliberately separate from
// DriverStepStore.Update: the latter is parent-owner fenced and cannot repair
// a lost projection after the parent DriverRun terminalizes.
type TerminalDriverStepRepairStore interface {
	RepairTerminalDriverStep(context.Context, TerminalDriverStepRepair) (*DriverStepRecord, bool, error)
}
