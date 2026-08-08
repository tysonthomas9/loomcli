package serve

import (
	"testing"
)

// ============================================================================
// log-router command registration tests
// ============================================================================

func TestLogRouterCmd_CommandName(t *testing.T) {
	if logRouterCmd.Name() != "log-router" {
		t.Errorf("command name = %q, want %q", logRouterCmd.Name(), "log-router")
	}
}

func TestLogRouterCmd_IsHidden(t *testing.T) {
	if !logRouterCmd.Hidden {
		t.Error("log-router command should be hidden, but Hidden = false")
	}
}

func TestLogRouterCmd_PersistentPreRunEOverridden(t *testing.T) {
	if logRouterCmd.PersistentPreRunE == nil {
		t.Fatal("log-router PersistentPreRunE should be set (overriding root), but is nil")
	}

	// The override should be a no-op (returns nil with no side effects).
	if err := logRouterCmd.PersistentPreRunE(logRouterCmd, nil); err != nil {
		t.Errorf("PersistentPreRunE() error = %v, want nil", err)
	}
}

// ============================================================================
// Required flags tests
// ============================================================================

func TestLogRouterCmd_AgentFlagRequired(t *testing.T) {
	flag := logRouterCmd.Flags().Lookup("agent")
	if flag == nil {
		t.Fatal("expected --agent flag to be defined on log-router command")
	}

	// cobra annotates required flags with "required" in the Annotations map
	annotations := flag.Annotations
	if annotations == nil {
		t.Fatal("--agent flag has no annotations, expected cobra.BashCompOneRequiredFlag")
	}
	requiredVals, ok := annotations["cobra_annotation_bash_completion_one_required_flag"]
	if !ok || len(requiredVals) == 0 || requiredVals[0] != "true" {
		t.Error("--agent flag should be marked as required")
	}
}

func TestLogRouterCmd_LockPathFlagRequired(t *testing.T) {
	flag := logRouterCmd.Flags().Lookup("lock-path")
	if flag == nil {
		t.Fatal("expected --lock-path flag to be defined on log-router command")
	}

	annotations := flag.Annotations
	if annotations == nil {
		t.Fatal("--lock-path flag has no annotations, expected cobra.BashCompOneRequiredFlag")
	}
	requiredVals, ok := annotations["cobra_annotation_bash_completion_one_required_flag"]
	if !ok || len(requiredVals) == 0 || requiredVals[0] != "true" {
		t.Error("--lock-path flag should be marked as required")
	}
}

// ============================================================================
// Optional flags and defaults tests
// ============================================================================

func TestLogRouterCmd_MaxLogSizeDefault(t *testing.T) {
	flag := logRouterCmd.Flags().Lookup("max-log-size")
	if flag == nil {
		t.Fatal("expected --max-log-size flag to be defined on log-router command")
	}
	if flag.DefValue != "50" {
		t.Errorf("--max-log-size default = %q, want %q", flag.DefValue, "50")
	}
}

func TestLogRouterCmd_BaseDirFlagOptional(t *testing.T) {
	flag := logRouterCmd.Flags().Lookup("base-dir")
	if flag == nil {
		t.Fatal("expected --base-dir flag to be defined on log-router command")
	}

	// base-dir should NOT be marked as required
	if annotations := flag.Annotations; annotations != nil {
		if vals, ok := annotations["cobra_annotation_bash_completion_one_required_flag"]; ok && len(vals) > 0 && vals[0] == "true" {
			t.Error("--base-dir flag should be optional, but is marked as required")
		}
	}

	if flag.DefValue != "" {
		t.Errorf("--base-dir default = %q, want empty string", flag.DefValue)
	}
}

func TestLogRouterCmd_UseString(t *testing.T) {
	if logRouterCmd.Use != "log-router" {
		t.Errorf("Use = %q, want %q", logRouterCmd.Use, "log-router")
	}
}

func TestLogRouterCmd_HasRunE(t *testing.T) {
	if logRouterCmd.RunE == nil {
		t.Error("log-router command should have RunE set, but it is nil")
	}
}
